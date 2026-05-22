package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Restaurant struct {
	Name      string
	DistanceM float64
	Minutes   int
	Lat       float64
	Lng       float64
}

type UserSession struct {
	Restaurants []Restaurant
	UpdatedAt   time.Time
}

type LineWebhookPayload struct {
	Destination string `json:"destination"`
	Events      []struct {
		Type       string `json:"type"`
		ReplyToken string `json:"replyToken"`
		Source     struct {
			UserID string `json:"userId"`
			Type   string `json:"type"`
		} `json:"source"`
		Message struct {
			Type      string  `json:"type"`
			ID        string  `json:"id"`
			Text      string  `json:"text,omitempty"`      
			Title     string  `json:"title,omitempty"`     
			Address   string  `json:"address,omitempty"`   
			Latitude  float64 `json:"latitude,omitempty"`  
			Longitude float64 `json:"longitude,omitempty"` 
		} `json:"message"`
	} `json:"events"`
}

var channelAccessToken string 
var channelSecret string       
var sessions sync.Map         
var userStates sync.Map 

func loadEnv(filename string) error {
	file, err := os.Open(filename)
	if err != nil { return err }
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") { continue }
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			os.Setenv(strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]))
		}
	}
	return scanner.Err()
}

func main() {
	if err := loadEnv("token.env"); err != nil {
		log.Fatalf("❌ 讀取 token.env 檔案失敗: %v", err)
	}
	channelAccessToken = os.Getenv("LINE_CHANNEL_ACCESS_TOKEN")
	channelSecret = os.Getenv("LINE_CHANNEL_SECRET")
	if channelAccessToken == "" || channelSecret == "" {
		log.Fatal("❌ LINE_CHANNEL_ACCESS_TOKEN 或 LINE_CHANNEL_SECRET 是空的！")
	}

	http.HandleFunc("/webhook", webhookHandler)
	port := os.Getenv("PORT")
	if port == "" { port = "5050" }
	log.Printf("🧪 [安全檢查] 原生代碼啟動成功。Listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func webhookHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("====== 收到來自 LINE 的 Webhook 請求 ======")
	lineSig := r.Header.Get("X-Line-Signature")
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Body error", http.StatusBadRequest)
		return
	}

	hash := hmac.New(sha256.New, []byte(channelSecret))
	hash.Write(bodyBytes)
	if lineSig != base64.StdEncoding.EncodeToString(hash.Sum(nil)) {
		log.Println("❌ [警告] 網路簽章不吻合，拒絕非法請求！")
		http.Error(w, "Invalid signature", http.StatusBadRequest)
		return
	}

	var payload LineWebhookPayload
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		w.WriteHeader(200); return
	}

	for _, ev := range payload.Events {
		if ev.Type == "message" {
			userID := ev.Source.UserID
			replyToken := ev.ReplyToken

			if ev.Message.Type == "location" {
				log.Printf("📍 收到位置: %s (%f, %f)", ev.Message.Title, ev.Message.Latitude, ev.Message.Longitude)
				go handleLocationRaw(context.Background(), replyToken, userID, ev.Message.Latitude, ev.Message.Longitude)
			} else if ev.Message.Type == "text" {
				go handleTextLogic(context.Background(), replyToken, userID, ev.Message.Text)
			}
		}
	}
	w.WriteHeader(200)
}

func handleTextLogic(ctx context.Context, replyToken, userID, text string) {
	trimmed := strings.TrimSpace(text)

	if trimmed == "新增地點" {
		userStates.Store(userID, "WAITING_FOR_DATA")
		
		msg1 := "請輸入欲新增的餐廳資訊，格式請嚴格遵守下方訊息範例（請直接複製修改，在冒號後直接打上資訊）："
		msg2 := "自定義餐廳名稱：\n餐廳的 google map 網址："

		type Message struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		type ReplyPayload struct {
			ReplyToken string    `json:"replyToken"`
			Messages   []Message `json:"messages"`
		}

		payload := ReplyPayload{
			ReplyToken: replyToken,
			Messages: []Message{
				{Type: "text", Text: msg1}, 
				{Type: "text", Text: msg2}, 
			},
		}

		sendReplyRaw(payload)
		return
	}

	state, inState := userStates.Load(userID)
	if inState && state.(string) == "WAITING_FOR_DATA" {
		handleAddLocationProcess(replyToken, userID, trimmed)
		return
	}

	handleText(ctx, replyToken, userID, text)
}

func handleAddLocationProcess(replyToken, userID, text string) {
	userStates.Delete(userID)

	lines := strings.Split(text, "\n")
	var name, mapURL string
	for _, l := range lines {
		if strings.Contains(l, "自定義餐廳名稱") {
			idx := strings.IndexAny(l, "：:")
			if idx != -1 {
				if string(l[idx:idx+3]) == "：" { name = l[idx+3:] } else { name = l[idx+1:] }
			}
		} else if strings.Contains(l, "餐廳的 google map 網址") || strings.Contains(l, "餐廳的google map 網址") {
			idx := strings.IndexAny(l, "：:")
			if idx != -1 {
				if string(l[idx:idx+3]) == "：" { mapURL = l[idx+3:] } else { mapURL = l[idx+1:] }
			}
		}
	}

	name = strings.TrimSpace(name)
	mapURL = strings.TrimSpace(mapURL)

	if name == "" || mapURL == "" {
		replyText(replyToken, "❌ 錯誤！輸入格式不正確或欄位缺失。\n請重新輸入「新增地點」再試一次！")
		return
	}

	if !strings.HasPrefix(mapURL, "http://") && !strings.HasPrefix(mapURL, "https://") {
		replyText(replyToken, "❌ 網址錯誤！請提供包含 http 或 https 的有效 Google Maps 連結。")
		return
	}

	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Timeout: 15 * time.Second,
		Jar:     jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
			return nil 
		},
	}

	req, err := http.NewRequest("GET", mapURL, nil)
	if err != nil { replyText(replyToken, "❌ 系統建立網路請求失敗"); return }
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")

	resp, err := client.Do(req)
	if err != nil { replyText(replyToken, "❌ 連線逾時！無法解析該網址。"); return }
	defer resp.Body.Close()

	// 1. 先抓最終跳轉網址
	finalURL := resp.Request.URL.String()
	log.Printf("🌐 最終網址停留點: %s", finalURL)

	var lat, lng string
	
	// 🎯 流派 A：嘗試從【網址字串】裡抓座標
	regAt := regexp.MustCompile(`@(-?\d+\.\d+),(-?\d+\.\d+)`)
	regBang := regexp.MustCompile(`!3d(-?\d+\.\d+).*!4d(-?\d+\.\d+)`)
	regQuery := regexp.MustCompile(`[?&](?:q|ll)=(-?\d+\.\d+),(-?\d+\.\d+)`)

	if match := regAt.FindStringSubmatch(finalURL); len(match) >= 3 {
		lat, lng = match[1], match[2]
	} else if match := regBang.FindStringSubmatch(finalURL); len(match) >= 3 {
		lat, lng = match[1], match[2]
	} else if match := regQuery.FindStringSubmatch(finalURL); len(match) >= 3 {
		lat, lng = match[1], match[2]
	}

	// 🌟 流派 B：如果網址列乾乾淨淨、什麼都沒有（就像這串頑固網址）
	// 我們直接把網頁內部的 HTML 程式碼全部下載下來，去源碼裡面撈被隱藏的座標參數！
	if lat == "" || lng == "" {
		log.Println("⚠️ 網址字串無座標特徵，啟動【真・網頁 HTML 源碼深度掃描】...")
		bodyBytes, err := io.ReadAll(resp.Body)
		if err == nil {
			htmlContent := string(bodyBytes)

			// 在 Google Maps 的網頁源碼中，經緯度常被塞在隱藏的 json 陣列或 meta 標籤裡
			// 特徵格式通常為: [25.0258, 121.5273] 或 "25.0258,121.5273"
			// 我們使用強力正則去網頁源碼中抓取符合經緯度範圍的浮點數配對
			regHTML := regexp.MustCompile(`(-?\d+\.\d{4,}),(-?\d+\.\d{4,})`)
			matches := regHTML.FindAllStringSubmatch(htmlContent, -1)

			for _, match := range matches {
				if len(match) >= 3 {
					tLat, _ := strconv.ParseFloat(match[1], 64)
					tLng, _ := strconv.ParseFloat(match[2], 64)
					
					// 台灣經緯度防呆校驗：緯度大約在 21~26 之間，經度在 119~123 之間
					if tLat > 21.0 && tLat < 26.0 && tLng > 119.0 && tLng < 123.0 {
						lat = fmt.Sprintf("%.6f", tLat)
						lng = fmt.Sprintf("%.6f", tLng)
						log.Printf("🎯 【源碼地底解鎖成功】成功從 HTML 內部挖出隱藏經緯度: %s, %s", lat, lng)
						break
					}
				}
			}
		}
	}

	// 🛑 雙重防禦依然失敗
	if lat == "" || lng == "" {
		replyText(replyToken, "❌ 座標解析失敗！此 Google 連結不包含任何可識別的位置特徵。\n提示：請確保是在 Google 地圖 App 餐廳主頁點擊「分享」複製的連結喔！")
		return
	}

	// 💾 寫入 Memory (restaurants.txt 檔案追加紀錄)
	f, err := os.OpenFile("restaurants.txt", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil { replyText(replyToken, "❌ 系統記憶體寫入失敗"); return }
	defer f.Close()

	record := fmt.Sprintf("%s|%s|%s\n", name, lat, lng)
	f.WriteString(record)

	log.Printf("💾 成功記憶新地點: %s (%s, %s)", name, lat, lng)
	replyText(replyToken, fmt.Sprintf("✅ 成功新增自訂地點！\n名稱：%s\n座標：%s, %s\n已同步至系統記憶體！", name, lat, lng))
}

func handleLocationRaw(ctx context.Context, replyToken, userID string, lat, lng float64) {
	latStr := fmt.Sprintf("%f", lat)
	lngStr := fmt.Sprintf("%f", lng)
	cmd := exec.CommandContext(ctx, "./engine", latStr, lngStr)
	out, err := cmd.Output()
	if err != nil {
		log.Printf("❌ C++ Engine 執行失敗: %v", err)
		replyText(replyToken, "系統呼叫 C++ 引擎時發生錯誤 ＞＜")
		return
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var nearby []Restaurant
	for _, line := range lines {
		if line == "NONE" || line == "" { continue }
		parts := strings.Split(line, "|")
		if len(parts) < 5 { continue }
		name := parts[0]
		distM, _ := strconv.ParseFloat(parts[1], 64)
		mins, _ := strconv.Atoi(parts[2]) 
		latF, _ := strconv.ParseFloat(parts[3], 64)
		lngF, _ := strconv.ParseFloat(parts[4], 64)
		if distM <= 2000 {
			nearby = append(nearby, Restaurant{Name: name, DistanceM: distM, Minutes: mins, Lat: latF, Lng: lngF})
		}
	}
	if len(nearby) == 0 {
		replyText(replyToken, "找不到 2 公里內的餐廳 ＞＜")
		return
	}
	sessions.Store(userID, UserSession{Restaurants: nearby, UpdatedAt: time.Now()})
	var b strings.Builder
	b.WriteString("幫您找到附近 2 公里內的推薦餐廳：\n\n")
	for i, r := range nearby {
		fmt.Fprintf(&b, "📍 %d. %s\n   距離：%.0fm (步行約 %d 分鐘)\n\n", i+1, r.Name, r.DistanceM, r.Minutes)
	}
	b.WriteString("💡 請點選下方的快捷鍵，我會把餐廳的地圖傳送給您喔！")

	type Action struct {
		Type  string `json:"type"`
		Label string `json:"label"`
		Text  string `json:"text"`
	}
	type QuickReplyItem struct {
		Type   string `json:"type"`
		Action Action `json:"action"`
	}
	type QuickReply struct {
		Items []QuickReplyItem `json:"items"`
	}
	type Message struct {
		Type       string      `json:"type"`
		Text       string      `json:"text"`
		QuickReply *QuickReply `json:"quickReply,omitempty"`
	}
	type ReplyPayload struct {
		ReplyToken string    `json:"replyToken"`
		Messages   []Message `json:"messages"`
	}
	var quickReplyItems []QuickReplyItem
	for _, r := range nearby {
		quickReplyItems = append(quickReplyItems, QuickReplyItem{
			Type: "action",
			Action: Action{Type: "message", Label: r.Name, Text: r.Name},
		})
	}
	payload := ReplyPayload{
		ReplyToken: replyToken,
		Messages: []Message{{Type: "text", Text: b.String(), QuickReply: &QuickReply{Items: quickReplyItems}}},
	}
	sendReplyRaw(payload)
}

func handleText(ctx context.Context, replyToken, userID, text string) {
	v, ok := sessions.Load(userID)
	if !ok {
		msg := "想尋找美食的話，點擊左下角「+」->「位置資訊」，分享您當前的地點，我會為您搜尋附近您已經儲存過的餐廳！\n\n" +
			"您也可以輸入「新增地點」來新增您的私房餐廳！"
		replyText(replyToken, msg)
		return
	}
	
	session, ok := v.(UserSession)
	if !ok {
		sessions.Delete(userID); return
	}

	if time.Since(session.UpdatedAt) > 15*time.Minute {
		sessions.Delete(userID)
		msg := "搜尋已連線逾時，請重新傳送位置訊息。\n\n" +
			"💡 提示：您也可以輸入「新增地點」來私藏新餐廳喔！"
		replyText(replyToken, msg)
		return
	}

	targetText := strings.TrimSpace(text)
	for _, r := range session.Restaurants {
		if strings.TrimSpace(r.Name) == targetText {
			type Message struct {
				Type      string  `json:"type"`
				Title     string  `json:"title"`
				Address   string  `json:"address"`
				Latitude  float64 `json:"latitude"`
				Longitude float64 `json:"longitude"`
			}
			type ReplyPayload struct {
				ReplyToken string    `json:"replyToken"`
				Messages   []Message `json:"messages"`
			}
			payload := ReplyPayload{
				ReplyToken: replyToken,
				Messages: []Message{{
					Type: "location", Title: r.Name, Address: "點擊查看地圖位置", Latitude: r.Lat, Longitude: r.Lng,
				}},
			}
			sendReplyRaw(payload)
			sessions.Delete(userID) 
			return
		}
	}
	
	replyText(replyToken, "找不到這家餐廳耶...請確認名稱是否正確，或重新傳送位置搜尋。")
}

func replyText(replyToken, text string) {
	type Message struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	type ReplyPayload struct {
		ReplyToken string    `json:"replyToken"`
		Messages   []Message `json:"messages"`
	}
	payload := ReplyPayload{ReplyToken: replyToken, Messages: []Message{{Type: "text", Text: text}}}
	sendReplyRaw(payload)
}

func sendReplyRaw(payload interface{}) {
	url := "https://api.line.me/v2/bot/message/reply"
	jsonData, err := json.Marshal(payload)
	if err != nil { log.Printf("❌ JSON 序列化失敗: %v", err); return }
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil { log.Printf("❌ 建立 HTTP 請求失敗: %v", err); return }
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+channelAccessToken)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil { log.Printf("❌ 發送 LINE 回覆失敗: %v", err); return }
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("❌ LINE 回傳錯誤狀態碼: %d", resp.StatusCode)
	} else {
		log.Println("✨ 成功透過原生 HTTP 回覆訊息！")
	}
}