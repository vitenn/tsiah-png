#!/bin/bash

# 檢查當前環境變數中是否有 LINE_CHANNEL_ACCESS_TOKEN
if [ -z "$LINE_CHANNEL_ACCESS_TOKEN" ]; then
    echo "❌ 錯誤：環境變數中未設定 LINE_CHANNEL_ACCESS_TOKEN！"
    echo "👉 請確認已將 Token 加入環境變數，或建立 token.env 檔案。"
    exit 1
fi

echo "1. 建立最新的 Rich Menu 物件..."
NEW_RICH_MENU_ID=$(curl -s -X POST https://api.line.me/v2/bot/richmenu \
-H "Authorization: Bearer $LINE_CHANNEL_ACCESS_TOKEN" \
-H "Content-Type: application/json" \
-d @rich_menu.json | grep -o '"richMenuId":"[^"]*' | cut -d'"' -f4)

if [ -z "$NEW_RICH_MENU_ID" ]; then
    echo "❌ 建立 Rich Menu 失敗，請檢查 token.env 是否正確載入。"
    exit 1
fi
echo "新的 Rich Menu ID: $NEW_RICH_MENU_ID"

echo "2. 上傳您的 rich_menu.png..."
UPLOAD_RESULT=$(curl -s -o /dev/null -w "%{http_code}" -X POST https://api-data.line.me/v2/bot/richmenu/$NEW_RICH_MENU_ID/content \
-H "Authorization: Bearer $LINE_CHANNEL_ACCESS_TOKEN" \
-H "Content-Type: image/png" \
-T rich_menu.png)

if [ "$UPLOAD_RESULT" != "200" ]; then
    echo "❌ 圖片上傳失敗！HTTP 狀態碼: $UPLOAD_RESULT"
    exit 1
fi

echo "3. 設定為預設選單..."
SET_RESULT=$(curl -s -o /dev/null -w "%{http_code}" -X POST https://api.line.me/v2/bot/user/all/richmenu/$NEW_RICH_MENU_ID \
-H "Authorization: Bearer $LINE_CHANNEL_ACCESS_TOKEN" \
-H "Content-Length: 0")

if [ "$SET_RESULT" != "200" ]; then
    echo "❌ 設定預設選單失敗！HTTP 狀態碼: $SET_RESULT"
    exit 1
fi

echo "✅ 成功將目前的 rich_menu.json 設定推送到 LINE！請重新開啟手機 LINE 聊天室。"
