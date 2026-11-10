package config

import (
	"errors"
	"github.com/spf13/viper"
)

type Config struct {
	Database struct {
		Type     string // 数据库类型，例如 "sqlite", "postgres", "mysql"
		Host     string
		Port     int
		User     string
		Password string
		DBName   string
		SSLMode  string
	}
	QzoneAPI struct {
		QRCodeURL  string
		LoginURL   string
		MessageURL string
	}
	Output struct {
		Dir     string // 导出目录（JSON / CSV / HTML），默认当前目录
		Verbose bool   // 打印每页抓取详情
	}
}

func LoadConfig() (*Config, error) {
	// 设置默认配置
	defaultConfig := &Config{
		Database: struct {
			Type     string
			Host     string
			Port     int
			User     string
			Password string
			DBName   string
			SSLMode  string
		}{
			Type:     "sqlite",
			Host:     "localhost",
			Port:     5432,
			User:     "your_db_user",
			Password: "your_db_password",
			DBName:   "./app.db", // 与原项目产物保持一致（旧版为 ./qzone-history.db）
			SSLMode:  "disable",
		},
		QzoneAPI: struct {
			QRCodeURL  string
			LoginURL   string
			MessageURL string
		}{
			QRCodeURL:  "https://ssl.ptlogin2.qq.com/ptqrshow?appid=549000912&e=2&l=M&s=3&d=72&v=4&t=0.8692955245720428&daid=5",
			LoginURL:   "https://ssl.ptlogin2.qq.com/ptqrlogin",
			MessageURL: "https://user.qzone.qq.com/proxy/domain/ic2.qzone.qq.com/cgi-bin/feeds/feeds2_html_pav_all",
		},
	}

	defaultConfig.Output.Dir = "."
	defaultConfig.Output.Verbose = false

	// 尝试读取外部配置文件
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("./config")

	if err := viper.ReadInConfig(); err != nil {
		var configFileNotFoundError viper.ConfigFileNotFoundError
		if errors.As(err, &configFileNotFoundError) {
			// 配置文件未找到，使用默认配置
			return defaultConfig, nil
		}
		// 其他错误
		return nil, err
	}

	// 将读取的配置与默认配置合并
	if err := viper.Unmarshal(defaultConfig); err != nil {
		return nil, err
	}

	return defaultConfig, nil
}
