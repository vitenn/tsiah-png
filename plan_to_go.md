## Plan: Go 版 LINE Bot 遷移

TL;DR：將現有以 `app.py`（FastAPI）與 `engine.cpp`（子程序）組成的 LINE Bot，完整以 Go 重新實作並保留外部介面（`POST /webhook`、LINE 訊息格式與 `./engine` subprocess 行為），採用最小改動路徑：先在 Go 中重建 webhook 與處理流程，暫時以 `os/exec` 呼叫現有 `engine`，之後視需求再內移 `engine` 邏輯。

**Steps**
1. 環境與憑證：將 `LINE_CHANNEL_ACCESS_TOKEN` 與 `LINE_CHANNEL_SECRET` 改為環境變數，並在 README 註明。 (*no deps*)
2. 建立專案骨架：`main.go`、初始化 `linebot.Client`、選定 router（建議 `echo`），設定 `POST /webhook` 路由與中介軟體。 (*depends on 1*)
3. Webhook 處理：實作簽名驗證、事件解析、事件分派（`handleLocation`、`handleText`）。(*depends on 2*)
4. 移植訊息處理邏輯：逐行轉譯 `handle_location` 與 `handle_text`，包含回覆格式（文字、地點卡）。 (*depends on 3*)
5. Session 管理：使用 `github.com/patrickmn/go-cache` 或 `sync.Map` + TTL 實作 `user_session`，確保並行安全。 (*parallel with 4*)
6. `engine` 整合：初期保留 `engine` 執行檔，使用 `os/exec` 呼叫並解析 stdout；可選後續工作：將 `engine.cpp` 邏輯以 Go 重寫，消除子程序。 (*depends on 4*)
7. 設定與記錄：用 `os.Getenv()` 讀取環境變數；採用 `log/slog` 或 `log` 做結構化日誌。 (*parallel with 2-6*)
8. 打包與建置：撰寫 Dockerfile（multi-stage），建立 make/run 指令（`go build`、`docker build`）。 (*depends on 2-7*)
9. 測試與驗證：撰寫單元測試（handlers、session、engine parsing）、整合測試（模擬 LINE webhook JSON）。 (*depends on 3-6*)
10. 文件與清理：更新 README、移除程式中硬編碼的 token、加入環境與部署指南。 (*depends on all above*)

**Relevant files**
- [app.py](app.py) — FastAPI webhook server，移植目標
- [engine.cpp](engine.cpp#L1-L80) — C++ 距離計算器，現階段以子程序保留
- [requirements.txt](requirements.txt) — 參考原先依賴
- [go.mod](go.mod) — 已宣告 `line-bot-sdk-go`，可重用
- [README.md](README.md) — 需更新的說明文件

**Verification**
- 啟動 Go 服務後，用 `curl` POST 範例 LINE event JSON 到 `POST /webhook`，確認回覆與現有 Python 版行為一致。
- 單元測試：針對 `handleLocation` 模擬 `engine` 回傳字串，驗證解析、篩選、排序與回覆內容。
- 整合測試：在本地啟動服務並用真實 LINE webhook 測試（或用官方 webhook 模擬工具）。
- 執行 `./engine`：確保編譯 `engine.cpp`（`g++ -O2 engine.cpp -o engine`）且 Go 服務可呼叫並解析 stdout。

**Decisions**
- 首階段保留 `engine` 子程序以縮短交付時間；之後再決定是否以 Go 重寫。  
- 建議 router：`echo`（輕量、直觀），也可選 `gin`（更成熟生態）。

**Further Considerations**
1. 是否要同時移除 `engine.cpp`（直接在 Go 內實作數學邏輯）？選項：A) 先保留子程序（快速）；B) 同步移植（一次到位）。推薦 A 先交付。  
2. 需否加入持久化（例如把餐廳改放 JSON/DB）？目前資料硬編碼在 `engine.cpp`，短期可保留。
3. 我可以接下來直接幫你開始實作 (scaffold + `POST /webhook` handler)。要我繼續嗎？
