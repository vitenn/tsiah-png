#include <iostream>
#include <cmath>
#include <cstdlib>
#include <cstring>
#include <cstdio>  // 💡 引入標準 I/O 庫來讀取檔案

#define PI 3.14159265358979323846
#define ROAD_CORRECTION_FACTOR 1.3
#define WALKING_SPEED_PER_MIN 75.0
#define MAX_RESTAURANTS 500 // 💡 放大上限，讓美食小金庫可以裝很多家餐廳

struct Restaurant {
    char name[100];
    double latitude;
    double longitude;
    double temp_dist; 
    int temp_mins;    
};

// 全域陣列，用來裝從檔案讀進來的動態資料
Restaurant my_map[MAX_RESTAURANTS];
int total_restaurants = 0; // 記錄目前硬碟裡到底存了幾家餐廳

void calculate_distance_and_time(double lat1, double lng1, double lat2, double lng2, double &distance, int &minutes) {
    double lat_mid = (lat1 + lat2) * PI / 180.0;
    double dx = (lng2 - lng1) * cos(lat_mid) * 111320.0;
    double dy = (lat2 - lat1) * 110574.0;
    double straight_distance = sqrt(dx * dx + dy * dy);
    distance = straight_distance * ROAD_CORRECTION_FACTOR;
    minutes = static_cast<int>(ceil(distance / WALKING_SPEED_PER_MIN));
}

// 💡 核心新增：從 Go 寫入的 restaurants.txt 載入餐廳資料
bool load_restaurants_from_file(const char* filename) {
    std::FILE* file = std::fopen(filename, "r");
    if (!file) {
        // 如果檔案不存在，可能還沒有人分享過網址，先給予預設資料避免當機
        return false;
    }

    char line[256];
    while (std::fgets(line, sizeof(line), file) && total_restaurants < MAX_RESTAURANTS) {
        // 移除換行符
        line[strcspn(line, "\r\n")] = 0;
        if (std::strlen(line) == 0) continue;

        // 用 sscanf 精準拆解 Go 寫入的格式： 名稱|0|0|緯度|經度
        // 運用 [^|] 語法來讀取含有空格的餐廳名稱，直到撞見第一個 | 號
        char name_buf[100] = {0};
        double lat = 0.0, lng = 0.0;
        int dummy_dist = 0, dummy_mins = 0;

        int parsed = std::sscanf(line, "%[^|]|%d|%d|%lf|%lf", name_buf, &dummy_dist, &dummy_mins, &lat, &lng);
        
        if (parsed == 5) {
            std::strncpy(my_map[total_restaurants].name, name_buf, sizeof(my_map[total_restaurants].name) - 1);
            my_map[total_restaurants].latitude = lat;
            my_map[total_restaurants].longitude = lng;
            my_map[total_restaurants].temp_dist = 0;
            my_map[total_restaurants].temp_mins = 0;
            total_restaurants++;
        }
    }
    std::fclose(file);
	return true;
}

int main(int argc, char* argv[]) {
    if (argc < 3) return 1;

    double user_lat = std::atof(argv[1]);
    double user_lng = std::atof(argv[2]);

    // 💡 1. 優先從硬碟檔案載入動態數據
    if (!load_restaurants_from_file("restaurants.txt") || total_restaurants == 0) {
        // Fallback 防禦機制：如果檔案讀不到，硬編碼塞兩家師大基礎店，確保 Demo 不會開天窗
        std::strncpy(my_map[0].name, "師大本部超大豬排飯", 99);
        my_map[0].latitude = 25.0258; my_map[0].longitude = 121.5273;
        std::strncpy(my_map[1].name, "公館校區救星義大利麵", 99);
        my_map[1].latitude = 25.0086; my_map[1].longitude = 121.5344;
        total_restaurants = 2;
    }

    // 2. 計算目前所有資料庫餐廳跟使用者的距離
    for (int i = 0; i < total_restaurants; i++) {
        calculate_distance_and_time(user_lat, user_lng, my_map[i].latitude, my_map[i].longitude, my_map[i].temp_dist, my_map[i].temp_mins);
    }

    // 3. 用選擇排序法 (Selection Sort) 排序實際載入的總餐廳數
    for (int i = 0; i < total_restaurants - 1; i++) {
        int min_idx = i;
        for (int j = i + 1; j < total_restaurants; j++) {
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

    // 4. 輸出符合兩公里內 (2000公尺) 的餐廳清單
    int valid_count = 0;
    for (int i = 0; i < total_restaurants; i++) {
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