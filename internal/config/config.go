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
	PortMeiligao  string
	PortXexun     string
	PortSuntech   string
	PortTotem     string
	PortGT02      string
	PortNavigil   string
	PortCastel    string

	// S3 Config
	S3Endpoint    string
	S3AccessKey   string
	S3SecretKey   string
	S3BucketName  string
	S3Region      string
	S3UseSSL      bool

	// JetStream Config
	JetstreamMaxAgeHours int
	JetstreamMaxBytes    int64
}
func Load() *Config {
	maxConns, _ := strconv.Atoi(getEnv("DB_MAX_CONNS", "50"))
	minConns, _ := strconv.Atoi(getEnv("DB_MIN_CONNS", "5"))
	useSSL, _ := strconv.ParseBool(getEnv("S3_USE_SSL", "false"))
	jsMaxAge, _ := strconv.Atoi(getEnv("JETSTREAM_MAX_AGE_HOURS", "48"))
	jsMaxBytes, _ := strconv.ParseInt(getEnv("JETSTREAM_MAX_BYTES", "4294967296"), 10, 64) // 4 GiB
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
		
		PortGT06:      getEnv("GT06_TCP_PORT", getEnv("PORT_GT06", "5023")),
		PortTeltonika: getEnv("TELTONIKA_TCP_PORT", getEnv("PORT_TELTONIKA", "5027")),
		PortCoban:     getEnv("TK103_TCP_PORT", getEnv("PORT_COBAN", "5013")),
		PortMeitrack:  getEnv("MEITRACK_TCP_PORT", getEnv("PORT_MEITRACK", "5020")),
		PortH02:       getEnv("H02_TCP_PORT", getEnv("PORT_H02", "5010")),
		PortMeiligao:  getEnv("MEILIGAO_TCP_PORT", "5002"),
		PortXexun:     getEnv("XEXUN_TCP_PORT", "5003"),
		PortSuntech:   getEnv("SUNTECH_TCP_PORT", "5017"),
		PortTotem:     getEnv("TOTEM_TCP_PORT", "5005"),
		PortGT02:      getEnv("GT02_TCP_PORT", "5006"),
		PortNavigil:   getEnv("NAVIGIL_TCP_PORT", "5012"),
		PortCastel:    getEnv("CASTEL_TCP_PORT", "5019"),

		S3Endpoint:    getEnv("S3_ENDPOINT", "localhost:9000"),
		S3AccessKey:   getEnv("S3_ACCESS_KEY", "minioadmin"),
		S3SecretKey:   getEnv("S3_SECRET_KEY", "minioadmin"),
		S3BucketName:  getEnv("S3_BUCKET_NAME", "adatrack-media"),
		S3Region:      getEnv("S3_REGION", "us-east-1"),
		S3UseSSL:      useSSL,

		JetstreamMaxAgeHours: jsMaxAge,
		JetstreamMaxBytes:    jsMaxBytes,
	}
}
func getEnv(key, fallback string) string {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		return val
	}
	return fallback
}
