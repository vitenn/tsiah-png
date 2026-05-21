# LINE Bot Project: 飽聲大帝（暫）

A vibe coding final project for the course Computer Programming II.  

## Python Dependencies for LINE Bot  
**Recommendations:** Installed in the virtual environment. 
Install the dependencies via the command below:
`pip install -r requirements.txt`

## LINE Bot in Go
This repo also contains a Go implementation of the LINE bot that keeps the original webhook interface and uses an external C++ engine for restaurant distance calculation.

### Build the Go server
1. Ensure Go 1.26 or later is installed.
2. Run:
```sh
go mod tidy
go build .
```

### Build the C++ engine
The C++ engine source is located in `cpp/engine.cpp`. Compile it into an executable named `engine`:
```sh
g++ -O2 cpp/engine.cpp -o engine
```

### Environment variables
Set the LINE credentials before running the Go server:
```sh
export LINE_CHANNEL_ACCESS_TOKEN=your_token
export LINE_CHANNEL_SECRET=your_secret
```

### Run the bot
```sh
./your_go_binary
```

### Notes
- The Go server calls `./engine` with latitude and longitude to calculate nearby restaurants.
- Keep `engine` in the same working directory as the Go executable when running.

