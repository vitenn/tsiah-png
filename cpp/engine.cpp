#include <iostream>
#include <cmath>
#include <cstdlib>
#include <cstring>

#define PI 3.14159265358979323846
#define ROAD_CORRECTION_FACTOR 1.3
#define WALKING_SPEED_PER_MIN 75.0
#define MAX_RESTAURANTS 5

struct Restaurant {
    char name[100];
    double latitude;
    double longitude;
    double temp_dist; // 暫存計算出來的距離
    int temp_mins;    // 暫存計算出來的時間
};

// 模擬使用者已經透過 UGC 機制儲存在我們系統裡的 5 家餐廳
Restaurant my_map[MAX_RESTAURANTS] = {
    {"師大本部超大豬排飯", 25.0258, 121.5273, 0, 0},
    {"古亭得獎滷肉飯", 25.0268, 121.5228, 0, 0},
    {"公館校區救星義大利麵", 25.0086, 121.5344, 0, 0},
    {"師大夜市多汁水煎包", 25.0245, 121.5290, 0, 0},
    {"水源市場高CP值炒飯", 25.0135, 121.5320, 0, 0}
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

    // 1. 先計算所有餐廳跟使用者的距離
    for (int i = 0; i < MAX_RESTAURANTS; i++) {
        calculate_distance_and_time(user_lat, user_lng, my_map[i].latitude, my_map[i].longitude, my_map[i].temp_dist, my_map[i].temp_mins);
    }

    // 2. 用選擇排序法 (Selection Sort) 依距離由近到遠排序 (不使用 STL)
    for (int i = 0; i < MAX_RESTAURANTS - 1; i++) {
        int min_idx = i;
        for (int j = i + 1; j < MAX_RESTAURANTS; j++) {
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

    // 3. 輸出符合兩公里內 (2000公尺) 的餐廳清單
    int valid_count = 0;
    for (int i = 0; i < MAX_RESTAURANTS; i++) {
        if (my_map[i].temp_dist <= 2000.0) {
            valid_count++;
            // 格式化輸出： 餐廳名稱 | 距離 | 分鐘 | 緯度 | 經度
            std::printf("%s|%.0f|%d|%.6f|%.6f\n", 
                        my_map[i].name, my_map[i].temp_dist, my_map[i].temp_mins, my_map[i].latitude, my_map[i].longitude);
        }
    }

    if (valid_count == 0) {
        std::printf("NONE\n");
    }

    return 0;
}