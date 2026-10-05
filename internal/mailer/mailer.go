package mailer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/smtp"
	"strings"

	"backend/internal/dbclient"
)

type SMTPConfig struct {
	Host      string `json:"host"`
	Port      int    `json:"port"`
	Username  string `json:"username"`
	Password  string `json:"password"`
	FromEmail string `json:"from_email"`
	FromName  string `json:"from_name"`
}

func GetSMTPConfig(ctx context.Context) (*SMTPConfig, error) {
	var val []byte
	err := dbclient.Pool.QueryRow(ctx, "SELECT setting_value FROM adatrack_gps_master.tm_global_settings WHERE setting_key = 'smtp_config'").Scan(&val)
	if err != nil {
		return nil, err
	}
	var cfg SMTPConfig
	if err := json.Unmarshal(val, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func SendEmail(ctx context.Context, to []string, subject, body string) error {
	cfg, err := GetSMTPConfig(ctx)
	if err != nil {
		return fmt.Errorf("failed to get smtp config: %w", err)
	}
	if cfg.Host == "" || cfg.FromEmail == "" {
		return fmt.Errorf("SMTP is not configured properly")
	}

	auth := smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)

	msg := []byte(fmt.Sprintf("To: %s\r\n"+
		"From: %s <%s>\r\n"+
		"Subject: %s\r\n"+
		"MIME-Version: 1.0\r\n"+
		"Content-Type: text/html; charset=\"UTF-8\"\r\n"+
		"\r\n"+
		"%s\r\n", strings.Join(to, ","), cfg.FromName, cfg.FromEmail, subject, body))

	return smtp.SendMail(addr, auth, cfg.FromEmail, to, msg)
}
