package main

import (
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
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/joho/godotenv"
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

// ✨ 定義 LINE Webhook 原生 JSON 結構，徹底擺脫 SDK 解析失敗的困擾
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
			Text      string  `json:"text,omitempty"`      // 文字訊息用
			Title     string  `json:"title,omitempty"`     // 位置訊息用
			Address   string  `json:"address,omitempty"`   // 位置訊息用
			Latitude  float64 `json:"latitude,omitempty"`  // 位置訊息用
			Longitude float64 `json:"longitude,omitempty"` // 位置訊息用
		} `json:"message"`
	} `json:"events"`
}

var channelAccessToken string 
var channelSecret string       
var sessions sync.Map         

func main() {
	err := godotenv.Load("token.env")
	if err != nil {
		log.Fatalf("❌ 讀取 token.env 檔案失敗: %v", err)
	}

	channelAccessToken = os.Getenv("LINE_CHANNEL_ACCESS_TOKEN")
	channelSecret = os.Getenv("LINE_CHANNEL_SECRET")
	if channelAccessToken == "" || channelSecret == "" {
		log.Fatal("❌ LINE_CHANNEL_ACCESS_TOKEN 或 LINE_CHANNEL_SECRET 是空的！")
	}

	http.HandleFunc("/webhook", webhookHandler)
	port := os.Getenv("PORT")
	if port == "" {
		port = "5050"
	}
	addr := ":" + port

	log.Printf("🧪 [安全檢查] Token/Secret 已載入，長度正確。")
	log.Printf("Listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}

func webhookHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("====== 收到來自 LINE 的 Webhook 請求 ======")

	lineSig := r.Header.Get("X-Line-Signature")
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Read body error", http.StatusBadRequest)
		return
	}

	// 驗證簽章
	hash := hmac.New(sha256.New, []byte(channelSecret))
	hash.Write(bodyBytes)
	expectedSig := base64.StdEncoding.EncodeToString(hash.Sum(nil))
	
	if lineSig != expectedSig {
		log.Println("❌ [警告] 網路簽章不吻合，拒絕非法請求！")
		http.Error(w, "Invalid signature", http.StatusBadRequest)
		return
	}
	log.Println("✅ 簽章完全吻合，通過安全檢查！")

	// 使用原生結構解析 JSON
	var payload LineWebhookPayload
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		log.Printf("❌ JSON 解析失敗: %v", err)
		w.WriteHeader(200)
		return
	}

	for _, ev := range payload.Events {
		if ev.Type == "message" {
			userID := ev.Source.UserID
			replyToken := ev.ReplyToken

			if ev.Message.Type == "location" {
				// ✨【功能實作】在終端機清晰列印使用者位置座標以利除錯
				log.Printf("📍 ===== 偵測到使用者發送位置 =====")
				log.Printf("📍 餐廳/位置名稱: %s", ev.Message.Title)
				log.Printf("📍 地址: %s", ev.Message.Address)
				log.Printf("📍 緯度 (Latitude): %f", ev.Message.Latitude)
				log.Printf("📍 經度 (Longitude): %f", ev.Message.Longitude)
				log.Printf("📍 ==================================")

				go handleLocationRaw(context.Background(), replyToken, userID, ev.Message.Latitude, ev.Message.Longitude)
			} else if ev.Message.Type == "text" {
				go handleText(context.Background(), replyToken, userID, ev.Message.Text)
			}
		}
	}

	w.WriteHeader(200)
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
		if line == "NONE" || line == "" {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) < 5 {
			continue
		}
		
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

	sessions.Store(userID, UserSession{
		Restaurants: nearby,
		UpdatedAt:   time.Now(),
	})

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
			Action: Action{
				Type:  "message",
				Label: r.Name,
				Text:  r.Name,
			},
		})
	}

	payload := ReplyPayload{
		ReplyToken: replyToken,
		Messages: []Message{
			{
				Type: "text",
				Text: b.String(),
				QuickReply: &QuickReply{
					Items: quickReplyItems,
				},
			},
		},
	}

	sendReplyRaw(payload)
}

func handleText(ctx context.Context, replyToken, userID, text string) {
	v, ok := sessions.Load(userID)
	if !ok {
		replyText(replyToken, "請先傳送您的「位置訊息」，我才能幫您尋找美食喔！")
		return
	}
	
	session, ok := v.(UserSession)
	if !ok {
		sessions.Delete(userID)
		return
	}

	if time.Since(session.UpdatedAt) > 15*time.Minute {
		sessions.Delete(userID)
		replyText(replyToken, "搜尋已連線逾時，請重新傳送位置訊息。")
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
				Messages: []Message{
					{
						Type:      "location",
						Title:     r.Name,
						Address:   "點擊查看地圖位置",
						Latitude:  r.Lat,
						Longitude: r.Lng,
					},
				},
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

	payload := ReplyPayload{
		ReplyToken: replyToken,
		Messages: []Message{
			{Type: "text", Text: text},
		},
	}
	sendReplyRaw(payload)
}

func sendReplyRaw(payload interface{}) {
	url := "https://api.line.me/v2/bot/message/reply"
	jsonData, err := json.Marshal(payload)
	if err != nil {
		log.Printf("❌ JSON 序列化失敗: %v", err)
		return
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		log.Printf("❌ 建立 HTTP 請求失敗: %v", err)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+channelAccessToken)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("❌ 發送 LINE 回覆失敗: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("❌ LINE 回傳錯誤狀態碼: %d", resp.StatusCode)
	} else {
		log.Println("✨ 成功透過原生 HTTP 回覆訊息！")
	}
}