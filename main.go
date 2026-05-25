package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"github.com/line/line-bot-sdk-go/v8/linebot"
)

type Restaurant struct {
	Name        string
	DistanceM   float64
	Minutes     int
	Lat         float64
	Lng         float64
	
	GoogleURL   string
	Rating      float64
	PhotoURL    string
}

var bot *linebot.Client
var sessions sync.Map   // map[userID]([]Restaurant)
var userStates sync.Map // map[userID]string，用來記錄使用者的上下文狀態

// 💡 定義 C++ 核心與 Go 共享的純文字資料庫路徑
const dbPath = "restaurants.txt"

func main() {
	token := os.Getenv("LINE_CHANNEL_ACCESS_TOKEN")
	secret := os.Getenv("LINE_CHANNEL_SECRET")

	if token == "" || secret == "" {
		log.Fatal("LINE_CHANNEL_ACCESS_TOKEN and LINE_CHANNEL_SECRET must be set")
	}

	var err error
	bot, err = linebot.New(secret, token)
	if err != nil {
		log.Fatalf("linebot.New: %v", err)
	}

	http.HandleFunc("/webhook", webhookHandler)
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	addr := ":" + port
	log.Printf("Listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}

func webhookHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("[====== Webhook 觸發了！ =====] 收到來自：%s 的請求", r.RemoteAddr)

	// 暫時性偵錯：記錄簽章與 request body 摘要，然後恢復 body 給 ParseRequest 使用
	sig := r.Header.Get("X-Line-Signature")
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("read body error: %v", err)
	} else {
		bstr := string(bodyBytes)
		if len(bstr) > 1000 {
			bstr = bstr[:1000] + "...(truncated)"
		}
		log.Printf("X-Line-Signature=%s, body=%s", sig, bstr)
		if os.Getenv("DEBUG_DUMP_BODY") == "1" {
			if writeErr := os.WriteFile("body.json", bodyBytes, 0644); writeErr != nil {
				log.Printf("write body.json error: %v", writeErr)
			} else {
				log.Printf("wrote debug body to body.json")
			}
			if writeErr := os.WriteFile("body.sig", []byte(sig), 0644); writeErr != nil {
				log.Printf("write body.sig error: %v", writeErr)
			}
			secret := os.Getenv("LINE_CHANNEL_SECRET")
			if secret != "" {
				h := hmac.New(sha256.New, []byte(secret))
				h.Write(bodyBytes)
				expected := base64.StdEncoding.EncodeToString(h.Sum(nil))
				log.Printf("expected signature=%s", expected)
			}
		}
	}
	// restore body for ParseRequest
	r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	events, err := bot.ParseRequest(r)
	if err != nil {
		if err == linebot.ErrInvalidSignature {
			log.Printf("ParseRequest error: %v, signature=%s", err, sig)
			http.Error(w, "Invalid signature", http.StatusBadRequest)
			return
		}
		log.Printf("ParseRequest error: %v", err)
		http.Error(w, "Parse error", http.StatusInternalServerError)
		return
	}
	log.Printf("ParseRequest succeeded, events=%d", len(events))

	ctx := r.Context()
	for _, ev := range events {
		if ev.Type == linebot.EventTypeMessage {
			switch msg := ev.Message.(type) {
			case *linebot.LocationMessage:
				go handleLocation(context.Background(), ev.ReplyToken, ev.Source.UserID, msg)
			case *linebot.TextMessage:
				go handleText(ctx, ev.ReplyToken, ev.Source.UserID, msg.Text)
			default:
				// ignore other message types for now
			}
		}
	}

	w.WriteHeader(200)
}

func handleLocation(ctx context.Context, replyToken, userID string, msg *linebot.LocationMessage) {
	// 🌟 1. 執行 C++ 核心程式，計算附近餐廳
	latStr := fmt.Sprintf("%f", msg.Latitude)
	lngStr := fmt.Sprintf("%f", msg.Longitude)
	cmd := exec.CommandContext(ctx, "./engine", latStr, lngStr)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			log.Printf("❌ C++ 核心崩潰原因 (stderr): %s", string(exitErr.Stderr))
		}
		log.Printf("engine error: %v", err)
		_, _ = bot.ReplyMessage(replyToken, linebot.NewTextMessage("計算餐廳時發生錯誤")).Do()
		return
	}

	// 🌟 2. 解析 C++ 回傳的餐廳資料
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var nearby []Restaurant
	for _, line := range lines {
		if line == "NONE" || line == "" {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) < 5 {
			continue
		}
		// 原本解析 C++ parts[0] 到 parts[4] 的部分保留
		name := parts[0]
		distM, _ := strconv.ParseFloat(parts[1], 64)
		minsF, _ := strconv.ParseFloat(parts[2], 64)
		latF, _ := strconv.ParseFloat(parts[3], 64)
		lngF, _ := strconv.ParseFloat(parts[4], 64)

		if distM <= 2000 { // 僅處理 2 公里內的餐廳
			// 組合 C++ 的距離資料
			res := Restaurant{Name: name, DistanceM: distM, Minutes: int(minsF + 0.5), Lat: latF, Lng: lngF}
			nearby = append(nearby, res)
		}
	}

	// 🌟 3. 讀取本機擴充資料庫，建立快速查詢 Map
	dbBytes, _ := os.ReadFile(dbPath) // 讀取本機資料庫檔案
	dbLines := strings.Split(string(dbBytes), "\n")
	extraInfoMap := make(map[string]Restaurant)

	for _, l := range dbLines {
		parts := strings.Split(strings.TrimSpace(l), "|")
		// 新格式只需 8 個欄位：名稱|0|0|緯度|經度|Google網址|評分|圖片網址
		if len(parts) >= 8 {
			r, _ := strconv.ParseFloat(parts[6], 64) // 解析評分
			extraInfoMap[parts[0]] = Restaurant{
				GoogleURL: parts[5],
				Rating:    r,
				PhotoURL:  parts[7],
			}
		}
	}

	// 🌟 4. 合併 C++ 資料與擴充資料庫
	for i := range nearby {
		if ext, exists := extraInfoMap[nearby[i].Name]; exists {
			// 如果擴充資料庫中有對應的餐廳，補充資料
			nearby[i].GoogleURL = ext.GoogleURL
			nearby[i].Rating = ext.Rating
			nearby[i].PhotoURL = ext.PhotoURL
		} else {
			// 預設防呆圖片
			nearby[i].PhotoURL = "https://images.unsplash.com/photo-1546069901-ba9599a7e63c?w=500"
		}
	}

	// 🌟 5. 組合 Flex Carousel 傳送 (取代原本的純文字回覆)
	var bubbles []*linebot.BubbleContainer
	for i, r := range nearby {
		if i >= 10 {
			break // LINE Carousel 一次最多只能發送 10 張卡片
		}
		bubbles = append(bubbles, createRestaurantBubble(r))
	}

	if len(bubbles) > 0 {
		// 建立 Flex Carousel
		carousel := &linebot.CarouselContainer{
			Type:     linebot.FlexContainerTypeCarousel,
			Contents: bubbles,
		}
		flexMsg := linebot.NewFlexMessage("吾為汝挑選的食肆名單", carousel)

		// 發送 Flex Message
		_, err := bot.ReplyMessage(replyToken, flexMsg).Do()
		if err != nil {
			log.Printf("發送 Flex Message 失敗: %v", err)
		} else {
			log.Println("✨ [食肆雷達] 成功發送精美 Flex Carousel！")
		}
	}

	// 檢查狀態
	state, ok := userStates.Load(userID)
	isBuaPue := ok && state.(string) == "waiting_for_bua_pue_loc"

	// 收到位置後，不管當初是什麼狀態，都清空它，避免卡住
	userStates.Delete(userID)

	if len(nearby) == 0 {
		if isBuaPue {
			_, _ = bot.ReplyMessage(replyToken, linebot.NewTextMessage("笑桮！這附近方圓兩公里內找不到餐廳！點擊「加入餐廳」告訴吾！")).Do()
		} else {
			_, _ = bot.ReplyMessage(replyToken, linebot.NewTextMessage("找不到 2 公里內的餐廳")).Do()
		}
		return
	}

	sessions.Store(userID, nearby)

	if isBuaPue {
		// 隨機盲抽附近的一家
		randomIndex := rand.Intn(len(nearby))
		chosen := nearby[randomIndex]

		replyMsg := fmt.Sprintf("聖桮！\n今晚就去吃【%s】！\n距離你 %.0f 公尺，走路大約 %d 分鐘就到了！", chosen.Name, chosen.DistanceM, chosen.Minutes)

		// 順便附上地圖導航按鈕給他
		bubble := &linebot.BubbleContainer{
			Type: linebot.FlexContainerTypeBubble,
			Body: &linebot.BoxComponent{
				Type:   linebot.FlexComponentTypeBox,
				Layout: linebot.FlexBoxLayoutTypeVertical,
				Contents: []linebot.FlexComponent{
					&linebot.TextComponent{
						Type:   linebot.FlexComponentTypeText,
						Text:   replyMsg,
						Wrap:   true,
						Weight: linebot.FlexTextWeightTypeBold,
					},
				},
			},
			Footer: &linebot.BoxComponent{
				Type:   linebot.FlexComponentTypeBox,
				Layout: linebot.FlexBoxLayoutTypeVertical,
				Contents: []linebot.FlexComponent{
					&linebot.ButtonComponent{
						Type:   linebot.FlexComponentTypeButton,
						Style:  linebot.FlexButtonStyleTypePrimary,
						Color:  "#00b900",
						Action: linebot.NewURIAction("開啟 Google Maps", fmt.Sprintf("https://www.google.com/maps/dir/?api=1&destination=%f,%f", chosen.Lat, chosen.Lng)),
					},
				},
			},
		}

		flexMsg := linebot.NewFlexMessage("大帝賜食", bubble)
		_, _ = bot.ReplyMessage(replyToken, flexMsg).Do()
		return
	}

	var b strings.Builder
	for i, r := range nearby {
		fmt.Fprintf(&b, "%d. %s — %.0fm, 約 %d 分鐘\n", i+1, r.Name, r.DistanceM, r.Minutes)
	}
	_, _ = bot.ReplyMessage(replyToken, linebot.NewTextMessage(b.String())).Do()
}

func handleText(ctx context.Context, replyToken, userID, text string) {
	// 防呆機制：如果使用者點了其他功能，就清空可能卡住的「跋桮」狀態
	if text == "加入餐廳" || text == "食肆雷達" || strings.Contains(text, "maps.app") {
		userStates.Delete(userID)
	}

	if text == "加入餐廳" {
		reply := linebot.NewTextMessage("請直接貼上 Google Maps 的餐廳網址！")
		reply.WithQuickReplies(linebot.NewQuickReplyItems(
			linebot.NewQuickReplyButton(
				"",
				&linebot.PostbackAction{
					Label:       "點我開啟鍵盤",
					Data:        "action=open_keyboard",
					InputOption: linebot.InputOptionOpenKeyboard,
				},
			),
		))
		_, _ = bot.ReplyMessage(replyToken, reply).Do()
		return
	}

	if text == "食肆雷達" {
		// 確保這時候如果傳送位置，是走正常的食肆雷達流程
		userStates.Store(userID, "waiting_for_radar_loc")

		reply := linebot.NewTextMessage("大帝的法眼已準備好！請傳送你現在的位置，讓大帝為你掃描周遭的隱藏美食！")
		reply.WithQuickReplies(linebot.NewQuickReplyItems(
			linebot.NewQuickReplyButton(
				"",
				linebot.NewLocationAction("點擊傳送我的位置"),
			),
		))
		_, _ = bot.ReplyMessage(replyToken, reply).Do()
		return
	}

	if text == "跋桮" {
		userStates.Store(userID, "waiting_for_bua_pue_loc")

		reply := linebot.NewTextMessage("請傳送你現在的位置，大帝將為你施法搜尋方圓內的命定餐廳！")
		reply.WithQuickReplies(linebot.NewQuickReplyItems(
			linebot.NewQuickReplyButton(
				"",
				linebot.NewLocationAction("點擊傳送我的位置"),
			),
		))
		_, _ = bot.ReplyMessage(replyToken, reply).Do()
		return
	}

	if strings.Contains(text, "maps.app.goo.gl") || strings.Contains(text, "goo.gl/maps") || strings.Contains(text, "google.com/maps") || strings.Contains(text, "google.com/maps/place") {
		// 提取出文字中的網址
		targetURL := extractURL(text)
		if targetURL == "" {
			_, _ = bot.ReplyMessage(replyToken, linebot.NewTextMessage("偵測到地圖格式，但解析不出有效網址。")).Do()
			return
		}

		// 呼叫核心解析函式
		restaurant, err := parseGoogleMaps(targetURL)
		if err != nil {
			log.Printf("解析 Google Maps 失敗: %v", err)
			_, _ = bot.ReplyMessage(replyToken, linebot.NewTextMessage("抱歉，這間餐廳的資訊太神祕了，我解析失敗...")).Do()
			return
		}

		// 2. 🗄️ 這裡呼叫你們的資料庫寫入邏輯
		err = saveToDatabase(restaurant)
		if err != nil {
			log.Printf("寫入資料庫失敗: %v", err)
			_, _ = bot.ReplyMessage(replyToken, linebot.NewTextMessage("解析成功，但存入美食小金庫時發生錯誤...")).Do()
			return
		}

		replyMsg := fmt.Sprintf("🎉 成功捕獲情勒新目標！\n名稱：%s\n經緯度：(%f, %f)\n已完美匯入美食底蘊資料庫！", restaurant.Name, restaurant.Lat, restaurant.Lng)
		_, _ = bot.ReplyMessage(replyToken, linebot.NewTextMessage(replyMsg)).Do()
		return
	}

	v, ok := sessions.Load(userID)
	if !ok {
		_, _ = bot.ReplyMessage(replyToken, linebot.NewTextMessage("請先傳送位置以搜尋附近餐廳")).Do()
		return
	}
	nearby, ok := v.([]Restaurant)
	if !ok {
		sessions.Delete(userID)
		_, _ = bot.ReplyMessage(replyToken, linebot.NewTextMessage("內部錯誤，請重新傳送位置")).Do()
		return
	}
	for _, r := range nearby {
		if strings.TrimSpace(r.Name) == strings.TrimSpace(text) {
			loc := linebot.NewLocationMessage(r.Name, "", r.Lat, r.Lng)
			_, _ = bot.ReplyMessage(replyToken, loc).Do()
			sessions.Delete(userID)
			return
		}
	}
	_, _ = bot.ReplyMessage(replyToken, linebot.NewTextMessage("找不到該餐廳，請確認名稱是否正確")).Do()
}

func extractURL(text string) string {
	words := strings.Fields(text)
	for _, word := range words {
		if strings.HasPrefix(word, "http://") || strings.HasPrefix(word, "https://") {
			return word
		}
	}
	return ""
}

func parseGoogleMaps(mapURL string) (Restaurant, error) {
	apiKey := os.Getenv("GOOGLE_MAPS_API_KEY")
	if apiKey == "" {
		return Restaurant{}, errors.New("需要 GOOGLE_MAPS_API_KEY")
	}

	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error { return nil }}
	resp, err := client.Get(mapURL)
	if err != nil {
		return Restaurant{}, fmt.Errorf("還原短網址失敗: %v", err)
	}
	defer resp.Body.Close()

	finalURL := resp.Request.URL.String()
	searchQuery := ""
	u, parseErr := url.Parse(finalURL)
	if parseErr == nil {
		if q := u.Query().Get("q"); q != "" {
			searchQuery = q
		}
	}
	if searchQuery == "" && strings.Contains(finalURL, "/maps/place/") {
		parts := strings.Split(finalURL, "/maps/place/")
		if len(parts) > 1 {
			subParts := strings.Split(parts[1], "/")
			if len(subParts) > 0 {
				if decoded, e := url.QueryUnescape(subParts[0]); e == nil {
					searchQuery = strings.ReplaceAll(decoded, "+", " ")
				}
			}
		}
	}
	if searchQuery == "" {
		searchQuery = finalURL
	}

	googleAPIURL := "https://places.googleapis.com/v1/places:searchText"
	jsonPayload := fmt.Sprintf(`{"textQuery": "%s", "languageCode": "zh-TW"}`, searchQuery)
	req, _ := http.NewRequest("POST", googleAPIURL, bytes.NewBuffer([]byte(jsonPayload)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Goog-Api-Key", apiKey)
	// 🌟 擴充 FieldMask，抓取更多資料
	req.Header.Set("X-Goog-FieldMask", "places.displayName,places.location,places.rating,places.googleMapsUri,places.photos")

	apiResp, err := client.Do(req)
	if err != nil {
		return Restaurant{}, fmt.Errorf("Google Places API 連線失敗: %v", err)
	}
	defer apiResp.Body.Close()

	type GooglePlacesResponse struct {
		Places []struct {
			Location struct {
				Latitude  float64 `json:"latitude"`
				Longitude float64 `json:"longitude"`
			} `json:"location"`
			DisplayName struct {
				Text string `json:"text"`
			} `json:"displayName"`
			Rating        float64 `json:"rating"`
			GoogleMapsUri string  `json:"googleMapsUri"`
			Photos        []struct {
				Name string `json:"name"`
			} `json:"photos"`
		} `json:"places"`
	}

	var gResp GooglePlacesResponse
	if err := json.NewDecoder(apiResp.Body).Decode(&gResp); err != nil {
		return Restaurant{}, fmt.Errorf("解析 Google JSON 失敗: %v", err)
	}

	if len(gResp.Places) > 0 {
		p := gResp.Places[0]
		res := Restaurant{
			Name:      p.DisplayName.Text,
			Lat:       p.Location.Latitude,
			Lng:       p.Location.Longitude,
			GoogleURL: p.GoogleMapsUri,
			Rating:    p.Rating,
		}
		
		// 組合照片 URL (如果有照片)
		if len(p.Photos) > 0 {
			res.PhotoURL = fmt.Sprintf("https://places.googleapis.com/v1/%s/media?key=%s&maxHeightPx=400&maxWidthPx=400", p.Photos[0].Name, apiKey)
		} else {
			res.PhotoURL = "https://images.unsplash.com/photo-1546069901-ba9599a7e63c?w=500" // 預設圖片
		}

		return res, nil
	}
	return Restaurant{}, errors.New("Google 官方 API 無法識別")
}

// 寫入檔案時，將新欄位用 `|` 串接在後面，C++ 引擎讀取前 5 個欄位不會受影響
func saveToDatabase(res Restaurant) error {
	f, err := os.OpenFile(dbPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	// 格式：名稱|0|0|緯度|經度|Google網址|評分|圖片網址
	record := fmt.Sprintf("%s|0|0|%f|%f|%s|%.1f|%s\n", res.Name, res.Lat, res.Lng, res.GoogleURL, res.Rating, res.PhotoURL)
	_, err = f.WriteString(record)
	return err
}

func createRestaurantBubble(res Restaurant) *linebot.BubbleContainer {
	// 1. 動態生成 5 顆星星
	var stars []linebot.FlexComponent
	ratingInt := int(res.Rating + 0.5) // 四捨五入
	for i := 1; i <= 5; i++ {
		iconURL := "https://developers-resource.landpress.line.me/fx/img/review_gold_star_28.png"
		if i > ratingInt {
			iconURL = "https://developers-resource.landpress.line.me/fx/img/review_gray_star_28.png"
		}
		stars = append(stars, &linebot.IconComponent{
			Type: linebot.FlexComponentTypeIcon,
			Size: linebot.FlexIconSizeTypeSm,
			URL:  iconURL,
		})
	}
	// 加上分數文字
	stars = append(stars, &linebot.TextComponent{
		Type:   linebot.FlexComponentTypeText,
		Text:   fmt.Sprintf("%.1f", res.Rating),
		Size:   linebot.FlexTextSizeTypeSm,
		Color:  "#999999",
		Margin: linebot.FlexComponentMarginTypeMd,
		Flex:   linebot.IntPtr(0),
	})

	// 2. 避免空網址導致點擊崩潰
	actionURI := res.GoogleURL
	if actionURI == "" {
		actionURI = "https://maps.google.com"
	}

	// 3. 建立並回傳完整 Bubble
	return &linebot.BubbleContainer{
		Type: linebot.FlexContainerTypeBubble,
		Hero: &linebot.ImageComponent{
			Type:        linebot.FlexComponentTypeImage,
			URL:         res.PhotoURL,
			Size:        linebot.FlexImageSizeTypeFull,
			AspectRatio: linebot.FlexImageAspectRatioType20to13,
			AspectMode:  linebot.FlexImageAspectModeTypeCover,
			Action:      linebot.NewURIAction("view image", actionURI),
		},
		Body: &linebot.BoxComponent{
			Type:   linebot.FlexComponentTypeBox,
			Layout: linebot.FlexBoxLayoutTypeVertical,
			Contents: []linebot.FlexComponent{
				&linebot.TextComponent{
					Type:   linebot.FlexComponentTypeText,
					Text:   res.Name,
					Weight: linebot.FlexTextWeightTypeBold,
					Size:   linebot.FlexTextSizeTypeXl,
				},
				&linebot.BoxComponent{
					Type:     linebot.FlexComponentTypeBox,
					Layout:   linebot.FlexBoxLayoutTypeBaseline,
					Margin:   linebot.FlexComponentMarginTypeMd,
					Contents: stars, // 塞入剛剛生成的星星陣列
				},
				&linebot.BoxComponent{
					Type:    linebot.FlexComponentTypeBox,
					Layout:  linebot.FlexBoxLayoutTypeVertical,
					Margin:  linebot.FlexComponentMarginTypeLg,
					Spacing: linebot.FlexComponentSpacingTypeSm,
					Contents: []linebot.FlexComponent{
						&linebot.BoxComponent{
							Type:    linebot.FlexComponentTypeBox,
							Layout:  linebot.FlexBoxLayoutTypeBaseline,
							Spacing: linebot.FlexComponentSpacingTypeSm,
							Contents: []linebot.FlexComponent{
								&linebot.TextComponent{Type: linebot.FlexComponentTypeText, Text: "距離", Color: "#aaaaaa", Size: linebot.FlexTextSizeTypeSm, Flex: linebot.IntPtr(2)},
								&linebot.TextComponent{Type: linebot.FlexComponentTypeText, Text: fmt.Sprintf("%.0f m (約 %d 分鐘)", res.DistanceM, res.Minutes), Wrap: true, Color: "#666666", Size: linebot.FlexTextSizeTypeSm, Flex: linebot.IntPtr(5)},
							},
						},
					},
				},
			},
		},
		Footer: &linebot.BoxComponent{
			Type:    linebot.FlexComponentTypeBox,
			Layout:  linebot.FlexBoxLayoutTypeVertical,
			Spacing: linebot.FlexComponentSpacingTypeSm,
			Contents: []linebot.FlexComponent{
				&linebot.ButtonComponent{
					Type:   linebot.FlexComponentTypeButton,
					Style:  linebot.FlexButtonStyleTypeLink,
					Height: linebot.FlexButtonHeightTypeSm,
					Action: linebot.NewURIAction("WEBSITE", actionURI),
				},
			},
			Flex: linebot.IntPtr(0),
		},
	}
}