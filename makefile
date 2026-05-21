all:
	go mod tidy;
	go build -o tsiah-png;
	g++ -O2 cpp/engine.cpp -o engine;