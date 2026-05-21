package main

import (
	"context"
	"fmt"
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
	bot, err = linebot.New(token, secret)
	if err != nil {
		log.Fatalf("linebot.New: %v", err)
	}

	http.HandleFunc("/webhook", webhookHandler)
	port := os.Getenv("PORT")
	if port == "" {
		port = "5050"
	}
	addr := ":" + port
	log.Printf("Listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}

func webhookHandler(w http.ResponseWriter, r *http.Request) {
	events, err := bot.ParseRequest(r)
	if err != nil {
		if err == linebot.ErrInvalidSignature {
			http.Error(w, "Invalid signature", http.StatusBadRequest)
			return
		}
		http.Error(w, "Parse error", http.StatusInternalServerError)
		return
	}

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
