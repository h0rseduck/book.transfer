package config

import (
	"fmt"
	"github.com/scorredoira/email"
	"net/mail"
	"net/smtp"
	"os"
)

func SendEmail(to string, subject string, body string, attachments map[string]string) error {
	from := os.Getenv("SMTP_LOGIN")
	auth := smtp.PlainAuth("", from, os.Getenv("SMTP_PASSWORD"), os.Getenv("SMTP_HOST"))

	message := email.NewMessage(subject, body)
	message.From = mail.Address{Name: "BookTracker", Address: from}
	message.To = []string{to}

	if len(attachments) > 0 {
		for _, path := range attachments {
			if err := message.Attach(path); err != nil {
				continue
			}
		}
	}

	addr := fmt.Sprintf("%s:%s", os.Getenv("SMTP_HOST"), os.Getenv("SMTP_PORT"))
	return email.Send(addr, auth, message)

	//message := gomail.NewMessage()
	//message.SetHeader("From", )
	//message.SetHeader("To", to)
	//message.SetHeader("Subject", subject)
	//message.SetBody("text/plain", body)
	//if len(attachments) > 0 {
	//	for name, path := range attachments {
	//		message.Attach(path, gomail.Rename(name))
	//	}
	//}
	//
	//port, _ := strconv.Atoi(os.Getenv("SMTP_PORT"))
	//dialer := gomail.NewDialer(, port, os.Getenv("SMTP_LOGIN"), )
	//
	//return dialer.DialAndSend(message)
}
