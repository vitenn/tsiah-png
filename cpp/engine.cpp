#include <iostream>
#include <cmath>
#include <cstdlib>
#include <cstring>
#include <fstream>
#include <sstream>
#include <vector>

#define PI 3.14159265358979323846
#define ROAD_CORRECTION_FACTOR 1.3
#define WALKING_SPEED_PER_MIN 75.0

struct Restaurant {
    char name[150];
    double latitude;
    double longitude;
    double temp_dist; 
    int temp_mins;    
};

void calculate_distance_and_time(double lat1, double lng1, double lat2, double lng2, double &distance, int &minutes) {
    double lat_mid = (lat1 + lat2) * PI / 180.0;
    double dx = (lng2 - lng1) * cos(lat_mid) * 111320.0;
    double dy = (lat2 - lat1) * 110574.0;
    double straight_distance = sqrt(dx * dx + dy * dy);
    distance = straight_distance * ROAD_CORRECTION_FACTOR;
    minutes = static_cast<int>(ceil(distance / WALKING_SPEED_PER_MIN));
}

int main(int argc, char* argv[]) {
    if (argc < 3) return 1;

    double user_lat = std::atof(argv[1]);
    double user_lng = std::atof(argv[2]);

    std::vector<Restaurant> my_map;

    // 📥 從硬碟讀取記憶體資料檔 restaurants.txt
    std::ifstream infile("restaurants.txt");
    std::string line;
    
    while (std::getline(infile, line)) {
        if (line.empty()) continue;
        std::stringstream ss(line);
        std::string name_str, lat_str, lng_str;
        
        // 檔案格式：餐廳名稱|緯度|經度
        if (std::getline(ss, name_str, '|') && 
            std::getline(ss, lat_str, '|') && 
            std::getline(ss, lng_str, '|')) {
            
            Restaurant r;
            std::strncpy(r.name, name_str.c_str(), sizeof(r.name) - 1);
            r.name[sizeof(r.name) - 1] = '\0';
            r.latitude = std::atof(lat_str.c_str());
            r.longitude = std::atof(lng_str.c_str());
            r.temp_dist = 0;
            r.temp_mins = 0;
            my_map.push_back(r);
        }
    }
    infile.close();

    // 💡 防呆機制：如果檔案全新沒資料，自動加載原本的預設 5 家餐廳當 Baseline
    if (my_map.empty()) {
        Restaurant defaults[5] = {
            {"師大本部超大豬排飯", 25.0258, 121.5273, 0, 0},
            {"古亭得獎滷肉飯", 25.0268, 121.5228, 0, 0},
            {"公館校區救星義大利麵", 25.0086, 121.5344, 0, 0},
            {"師大夜市多汁水煎包", 25.0245, 121.5290, 0, 0},
            {"水源市場高CP值炒飯", 25.0135, 121.5320, 0, 0}
        };
        for(int i = 0; i < 5; i++) my_map.push_back(defaults[i]);
    }

    // 1. 計算所有餐廳跟使用者的距離
    for (size_t i = 0; i < my_map.size(); i++) {
        calculate_distance_and_time(user_lat, user_lng, my_map[i].latitude, my_map[i].longitude, my_map[i].temp_dist, my_map[i].temp_mins);
    }

    // 2. 用選擇排序法 (Selection Sort) 排序 (不使用 STL sort)
    int n = my_map.size();
    for (int i = 0; i < n - 1; i++) {
        int min_idx = i;
        for (int j = i + 1; j < n; j++) {
            if (my_map[j].temp_dist < my_map[min_idx].temp_dist) {
                min_idx = j;
            }
        }
        if (min_idx != i) {
            Restaurant temp = my_map[i];
            my_map[i] = my_map[min_idx];
            my_map[min_idx] = temp;
        }
    }

    // 3. 輸出符合兩公里內的餐廳
    int valid_count = 0;
    for (size_t i = 0; i < my_map.size(); i++) {
        if (my_map[i].temp_dist <= 2000.0) {
            valid_count++;
            std::printf("%s|%.0f|%d|%.6f|%.6f\n", 
                        my_map[i].name, my_map[i].temp_dist, my_map[i].temp_mins, my_map[i].latitude, my_map[i].longitude);
        }
    }

    if (valid_count == 0) {
        std::printf("NONE\n");
    }

    return 0;
}