package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net/http"
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
				go handleLocation(ctx, ev.ReplyToken, ev.Source.UserID, msg)
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
