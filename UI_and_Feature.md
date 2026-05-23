# UI/UX and Interactive Features

## I. Flex Messages
- 回傳格式以 **Flex Message Carousel（橫幅滑動卡片）** 為主，呈現附近餐廳資訊，包含餐廳名稱、地址、評分等。
## II. Rich Menu（常駐型圖文選單）
> ### 注意！  
> 如果是第一次使用，或是有更動到 `rich_menu.json`，請務必執行 `./push_rich_menu.sh` 或使用 `make menu` 指令來更新 LINE 的圖文選單（Rich Menu）設定。
### Buttons  
1. **食肆雷達：** Flex Message Carousel（橫幅滑動卡片）顯示附近餐廳。  
    - 要求使用者傳送位置，回傳附近餐廳資訊。  
    - 刪除功能：增加「刪除」按鈕，使用者點選後再（回傳選單）詢問要刪除哪一家餐廳，使用者點選後刪除該餐廳資料。  

2. **跋桮：** 取得位置，隨機從周圍選出一間餐廳
    - 聖桮：在資料庫中有找到餐廳。
    - 笑桮：在資料庫中沒有找到餐廳。  

3. **加入餐廳**  
    - 使用者點擊按鈕， bot 回傳文字「請把餐廳的 Google Maps 網址傳給吾」。跳出鍵盤提供使用者輸入餐廳網址，若偵測到非 Google Maps 網址，則回傳「請提供有效的 Google Maps 網址」。

4. **我的收藏（資料庫中所有餐廳）**
    - Flex Message Carousel（橫幅滑動卡片）。