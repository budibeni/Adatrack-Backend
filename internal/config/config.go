package config
import (
	"os"
	"strconv"
)
type Config struct {
	DBUser            string
	DBPass            string
	DBHost            string
	DBPort            string
	DBName            string
	DBMaxConns        int
	DBMinConns        int
	RedisAddr         string
	RedisPassword     string
	NatsURL           string
	JWTSecret         string
	PortWebsocket     string
	
	// GPS Protocol Ports
	PortGT06      string
	PortTeltonika string
	PortCoban     string
	PortMeitrack  string
	PortH02       string

	// S3 Config
	S3Endpoint    string
	S3AccessKey   string
	S3SecretKey   string
	S3BucketName  string
	S3Region      string
	S3UseSSL      bool
}
func Load() *Config {
	maxConns, _ := strconv.Atoi(getEnv("DB_MAX_CONNS", "50"))
	minConns, _ := strconv.Atoi(getEnv("DB_MIN_CONNS", "5"))
	useSSL, _ := strconv.ParseBool(getEnv("S3_USE_SSL", "false"))
	return &Config{
		DBUser:        getEnv("DB_USER", "adatrack_local"),
		DBPass:        getEnv("DB_PASSWORD", "local_password"),
		DBHost:        getEnv("DB_HOST", "localhost"),
		DBPort:        getEnv("DB_PORT", "5432"),
		DBName:        getEnv("DB_NAME", "adatrack_gps_master"),
		DBMaxConns:    maxConns,
		DBMinConns:    minConns,
		RedisAddr:     getEnv("REDIS_ADDR", "localhost:6379"),
		RedisPassword: getEnv("REDIS_PASSWORD", ""),
		NatsURL:       getEnv("NATS_URL", "nats://localhost:4222"),
		JWTSecret:     getEnv("JWT_SECRET", "super-secret-key-change-me"),
		PortWebsocket: getEnv("PORT_WEBSOCKET", "8080"),
		
		PortGT06:      getEnv("PORT_GT06", "15000"),
		PortTeltonika: getEnv("PORT_TELTONIKA", "15001"),
		PortCoban:     getEnv("PORT_COBAN", "15002"),
		PortMeitrack:  getEnv("PORT_MEITRACK", "15003"),
		PortH02:       getEnv("PORT_H02", "15004"),

		S3Endpoint:    getEnv("S3_ENDPOINT", "localhost:9000"),
		S3AccessKey:   getEnv("S3_ACCESS_KEY", "minioadmin"),
		S3SecretKey:   getEnv("S3_SECRET_KEY", "minioadmin"),
		S3BucketName:  getEnv("S3_BUCKET_NAME", "adatrack-media"),
		S3Region:      getEnv("S3_REGION", "us-east-1"),
		S3UseSSL:      useSSL,
	}
}
func getEnv(key, fallback string) string {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		return val
	}
	return fallback
}
