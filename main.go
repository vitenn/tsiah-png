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
	Name      string
	DistanceM float64
	Minutes   int
	Lat       float64
	Lng       float64
}

var bot *linebot.Client
var sessions sync.Map // map[userID]([]Restaurant)

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
		name := parts[0]
		distM, _ := strconv.ParseFloat(parts[1], 64)
		minsF, _ := strconv.ParseFloat(parts[2], 64)
		latF, _ := strconv.ParseFloat(parts[3], 64)
		lngF, _ := strconv.ParseFloat(parts[4], 64)
		if distM <= 2000 {
			nearby = append(nearby, Restaurant{Name: name, DistanceM: distM, Minutes: int(minsF + 0.5), Lat: latF, Lng: lngF})
		}
	}

	if len(nearby) == 0 {
		_, _ = bot.ReplyMessage(replyToken, linebot.NewTextMessage("找不到 2 公里內的餐廳")).Do()
		return
	}

	sessions.Store(userID, nearby)

	var b strings.Builder
	for i, r := range nearby {
		fmt.Fprintf(&b, "%d. %s — %.0fm, 約 %d 分鐘\n", i+1, r.Name, r.DistanceM, r.Minutes)
	}
	_, _ = bot.ReplyMessage(replyToken, linebot.NewTextMessage(b.String())).Do()
}

func handleText(ctx context.Context, replyToken, userID, text string) {
	if strings.Contains(text, "maps.app.goo.gl") || strings.Contains(text, "goo.gl/maps") || strings.Contains(text, "google.com/maps") || strings.Contains(text, "google.com/maps/place") {
		// 提取出文字中的網址
		targetURL := extractURL(text)
		if targetURL == "" {
			_, _ = bot.ReplyMessage(replyToken, linebot.NewTextMessage("偵測到地圖格式，但解析不出有效網址。")).Do()
			return
		}

		// 呼叫核心解析函式
		name, lat, lng, err := parseGoogleMaps(targetURL)
		if err != nil {
			log.Printf("解析 Google Maps 失敗: %v", err)
			_, _ = bot.ReplyMessage(replyToken, linebot.NewTextMessage("抱歉，這間餐廳的經緯度太神祕了，我解析失敗...")).Do()
			return
		}

		// 2. 🗄️ 這裡呼叫你們的資料庫寫入邏輯
		err = saveToDatabase(name, lat, lng)
		if err != nil {
			log.Printf("寫入資料庫失敗: %v", err)
			_, _ = bot.ReplyMessage(replyToken, linebot.NewTextMessage("解析成功，但存入美食小金庫時發生硬碟錯誤...")).Do()
			return
		}

		replyMsg := fmt.Sprintf("🎉 成功捕獲情勒新目標！\n名稱：%s\n經緯度：(%f, %f)\n已完美匯入美食底蘊資料庫！", name, lat, lng)
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

func parseGoogleMaps(mapURL string) (name string, lat, lng float64, err error) {
	apiKey := os.Getenv("GOOGLE_MAPS_API_KEY")
	if apiKey == "" {
		return "", 0, 0, errors.New("需要 GOOGLE_MAPS_API_KEY")
	}

	// 🛠️ 步驟 1：先用 http.Client 追蹤重導向，把短網址還原成真實長網址
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return nil // 允許跟隨 302 導向
		},
	}

	resp, err := client.Get(mapURL)
	if err != nil {
		return "", 0, 0, fmt.Errorf("還原短網址失敗: %v", err)
	}
	defer resp.Body.Close()

	finalURL := resp.Request.URL.String()
	log.Printf("🔗 短網址已成功還原為長網址: %s", finalURL)

	// 🛠️ 步驟 2：從長網址中抽離出搜尋關鍵字 (包含可能很髒的店名或地址)
	searchQuery := ""
	u, parseErr := url.Parse(finalURL)
	if parseErr == nil {
		if q := u.Query().Get("q"); q != "" {
			searchQuery = q
		}
	}

	// 防禦機制：如果從 query 沒撈到，試著從 /maps/place/ 路徑撈
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

	// 如果真的不幸什麼字串都撈不到，最後的死馬當活馬醫：直接把整串長網址餵給 Google 搜尋
	if searchQuery == "" {
		searchQuery = finalURL
	}

	log.Printf("📡 正在將還原後的關鍵字 [%s] 交付 Google Places API 進行精準洗滌...", searchQuery)

	// 🛠️ 步驟 3：呼叫 Google Places API (New) 換取最乾淨的店名與座標
	googleAPIURL := "https://places.googleapis.com/v1/places:searchText"
	jsonPayload := fmt.Sprintf(`{"textQuery": "%s", "languageCode": "zh-TW"}`, searchQuery)

	req, _ := http.NewRequest("POST", googleAPIURL, bytes.NewBuffer([]byte(jsonPayload)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Goog-Api-Key", apiKey)
	req.Header.Set("X-Goog-FieldMask", "places.displayName,places.location")

	apiResp, err := client.Do(req)
	if err != nil {
		return "", 0, 0, fmt.Errorf("Google Places API 連線失敗: %v", err)
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
		} `json:"places"`
	}

	var gResp GooglePlacesResponse
	if err := json.NewDecoder(apiResp.Body).Decode(&gResp); err != nil {
		return "", 0, 0, fmt.Errorf("解析 Google JSON 失敗: %v", err)
	}

	if len(gResp.Places) > 0 {
		targetPlace := gResp.Places[0]
		name = targetPlace.DisplayName.Text // ✨ 這裡拿到的絕對是100%最乾淨、無地址贅字的店名
		lat = targetPlace.Location.Latitude
		lng = targetPlace.Location.Longitude
		log.Printf("🎉 混合架構整合成功！店名: [%s], 座標: (%f, %f)", name, lat, lng)
		return name, lat, lng, nil
	}

	return "", 0, 0, errors.New("Google 官方 API 無法從此地圖關鍵字識別任何地點")
}

func saveToDatabase(name string, lat, lng float64) error {
	// 以 O_APPEND (附加) 模式開啟檔案，若檔案不存在則自動 O_CREATE 建立
	f, err := os.OpenFile(dbPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	// 依照你們 C++ 核心對齊的格式寫入：名稱|0|0|緯度|經度
	// 這裡的 0 和 0 是預留給 C++ 計算距離和時間的欄位
	record := fmt.Sprintf("%s|0|0|%f|%f\n", name, lat, lng)
	_, err = f.WriteString(record)
	return err
}
