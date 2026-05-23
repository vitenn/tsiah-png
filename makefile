all:
	go mod tidy;
	go build -o tsiah-png;
	g++ -O2 cpp/engine.cpp -o engine;

# 更新 LINE Rich Menu (圖文選單)
menu:
	./push_rich_menu.sh