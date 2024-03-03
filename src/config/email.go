package config

import (
	"gopkg.in/gomail.v2"
	"os"
	"strconv"
)

func SendEmail(to string, subject string, body string, attachments map[string]string) error {
	message := gomail.NewMessage()
	message.SetHeader("From", os.Getenv("SMTP_LOGIN"))
	message.SetHeader("To", to)
	message.SetHeader("Subject", subject)
	message.SetBody("text/plain", body)
	if len(attachments) > 0 {
		for name, path := range attachments {
			message.Attach(path, gomail.Rename(name))
		}
	}

	port, _ := strconv.Atoi(os.Getenv("SMTP_PORT"))
	dialer := gomail.NewDialer(os.Getenv("SMTP_HOST"), port, os.Getenv("SMTP_LOGIN"), os.Getenv("SMTP_PASSWORD"))

	return dialer.DialAndSend(message)
}
