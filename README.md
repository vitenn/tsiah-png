# LINE Bot Project: 飽聲大帝（暫）

A vibe coding final project for the course Computer Programming II.  
This project uses **Go** and **C++** as main programming languages. 

### How to Build and Run
#### Step I. Build the Go server

> You can directly run the **makefile** to compile Go and C++ file. 

1. Ensure Go 1.26 or later is installed.
2. Run:
```sh
go mod tidy;
go build -o tsiah-png;
```

#### Step II. Build the C++ engine
The C++ engine source is located in `cpp/engine.cpp`. Compile it into an executable named `engine`:
```sh
g++ -O2 cpp/engine.cpp -o engine
``` 

#### Step III. Set the Environment Variables
Set the **LINE** credentials before running the Go server.  
As this project also uses **Google Maps API** to fetch restaurant data, you also need to set the Google Maps API key as an environment variable.
```sh
export LINE_CHANNEL_ACCESS_TOKEN="your_token";
export LINE_CHANNEL_SECRET="your_secret";
export GOOGLE_MAPS_API_KEY="your_google_maps_api_key";
```

#### Step IV. Run the bot
```sh
./tsiah-png
```

### Notes
- The Go server calls `./engine` with latitude and longitude to calculate nearby restaurants.
- Keep `engine` in the same working directory as the Go executable when running.