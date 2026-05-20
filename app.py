import subprocess
from fastapi import FastAPI, Request, HTTPException
from linebot import LineBotApi, WebhookHandler
from linebot.exceptions import InvalidSignatureError
from linebot.models import MessageEvent, LocationMessage, TextMessage, TextSendMessage, LocationSendMessage
app = FastAPI()

# ⚠️ 請確保這裡有填入你的憑證
LINE_CHANNEL_ACCESS_TOKEN = "jwRW6/DD9UT16ZwnsC6GXeozxQcn3ndN3/3svfuX7WINNV6GoWlPEdlN3Jqhlr4TBDlB+Vcctx5TO6PXvk+wNHdwnhlzSrkz9EI/RINdNJ+ISXaA59HuNE1jiVyNzeIyuUPm1H+fcsLw/QjmRFChXgdB04t89/1O/w1cDnyilFU="
LINE_CHANNEL_SECRET = "f6743732d7cc1e9cd7a4fea722392301"

line_bot_api = LineBotApi(LINE_CHANNEL_ACCESS_TOKEN)
handler = WebhookHandler(LINE_CHANNEL_SECRET)

# 💡 記憶體狀態機：用來記錄該使用者目前週邊有哪些餐廳可以選
# 在實務上會用 user_id 當 key，這裡我們示範單人體驗
user_session = {
    "nearby_restaurants": {} # 格式: {"餐廳名稱": (lat, lng)}
}

@app.post("/webhook")
async def callback(request: Request):
    signature = request.headers.get("X-Line-Signature")
    body = await request.body()
    try:
        handler.handle(body.decode("utf-8"), signature)
    except InvalidSignatureError:
        raise HTTPException(status_code=400, detail="Invalid signature")
    return "OK"

# 情境 A：使用者傳送位置資訊 -> 由近到遠列出兩公里內的自訂餐廳
@handler.add(MessageEvent, message=LocationMessage)
def handle_location(event):
    user_lat = event.message.latitude
    user_lng = event.message.longitude
    
    # 呼叫 C++ 引擎進行排序
    try:
        result = subprocess.run(
            ["./engine", str(user_lat), str(user_lng)], 
            capture_output=True, text=True, encoding="utf-8"
        )
        lines = result.stdout.strip().split("\n")
    except Exception as e:
        line_bot_api.reply_message(event.reply_token, TextSendMessage(text="後端引擎異常 💥"))
        return

    if not lines or lines[0] == "NONE" or lines[0] == "":
        reply_text = "附近 2 公里內居然沒有你的愛店？！🙄 肚子這麼餓，還不快去多開發新標籤！"
        user_session["nearby_restaurants"] = {} # 清空
    else:
        reply_text = "點餐時間到！以下是附近 2 公里內你存過的愛店（已由近到遠排序）：\n\n"
        current_nearby = {}
        
        for idx, line in enumerate(lines):
            # 解析 C++ 吐回來的結構化資料
            name, dist, mins, r_lat, r_lng = line.split("|")
            reply_text += f"{idx+1}. 【{name}】\n📍 距離：{dist} 公尺\n🚶 步行：約 {mins} 分鐘\n\n"
            
            # 存入 Session 供後續文字比對
            current_nearby[name] = (float(r_lat), float(r_lng))
        
        reply_text += "👉 請直接輸入你想去的「餐廳完整名稱」，我直接把導航地圖丟給你！\n（或是重新傳送定位以更新列表）"
        user_session["nearby_restaurants"] = current_nearby

    line_bot_api.reply_message(event.reply_token, TextSendMessage(text=reply_text))

# 情境 B：使用者輸入餐廳名字 -> 丟出該餐廳的地圖卡片
@handler.add(MessageEvent, message=TextMessage)
def handle_text(event):
    user_message = event.message.text.strip()
    saved_list = user_session.get("nearby_restaurants", {})

    # 檢查使用者的文字是不是剛剛列出的餐廳之一
    if user_message in saved_list:
        target_lat, target_lng = saved_list[user_message]
        
        # 傳送 LINE 內建的「位置訊息卡片」，使用者點開可以直接打開 Google Map 導航！
        line_bot_api.reply_message(
            event.reply_token,
            LocationSendMessage(
                title=user_message,
                address=f"從你剛剛的位置步行即可抵達",
                latitude=target_lat,
                longitude=target_lng
            )
        )
        # 用完後清空狀態
        user_session["nearby_restaurants"] = {}
    else:
        # 如果不是餐廳名，丟出預設情勒
        line_bot_api.reply_message(
            event.reply_token,
            TextSendMessage(text=f"聽不懂你在說什麼，現在限你立刻傳送位置過來，不然就去吃巷口吃膩的乾麵！🤪")
        )

if __name__ == "__main__":
    import uvicorn
    uvicorn.run(app, host="0.0.0.0", port=5050)