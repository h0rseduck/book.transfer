package telegram

import (
	"book.transfer/src/config"
	"database/sql"
	"errors"
	"fmt"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"io"
	"log"
	"net/http"
	"net/mail"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type TransferService struct {
	Bot             tgbotapi.BotAPI
	db              *sql.DB
	allowChatIds    map[int64]bool
	allowExtensions map[string]bool
}

func NewTransferService() *TransferService {
	bot, err := tgbotapi.NewBotAPI(os.Getenv("TELEGRAM_TOKEN"))
	if err != nil {
		log.Fatal(err)
	}
	//bot.Debug = true

	var (
		allowChatIds    = map[int64]bool{}
		allowExtensions = map[string]bool{}
		i               int64
	)
	for _, id := range strings.Split(os.Getenv("ALLOW_IDS"), ";") {
		i, err = strconv.ParseInt(id, 10, 64)
		if err != nil {
			continue
		}
		allowChatIds[i] = true
	}
	for _, ext := range strings.Split(os.Getenv("ALLOW_EXTENSION"), ";") {
		allowExtensions[ext] = true
	}

	db := config.GetDB()
	return &TransferService{Bot: *bot, db: db, allowChatIds: allowChatIds, allowExtensions: allowExtensions}
}

func (s *TransferService) ListenForWebhook() {
	updates := s.Bot.ListenForWebhook("/" + os.Getenv("TELEGRAM_WEBHOOK"))

	go http.ListenAndServe("0.0.0.0:3000", nil)

	for update := range updates {
		if !s.allowChatIds[update.Message.From.ID] {
			continue
		}
		if update.Message.IsCommand() {
			s.command(update.Message)
		} else if update.Message.Document != nil {
			s.document(update.Message)
		}
	}
}

func (s *TransferService) Observe() {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 15

	updates := s.Bot.GetUpdatesChan(u)
	for update := range updates {
		if !s.allowChatIds[update.Message.From.ID] {
			continue
		}
		if update.Message.IsCommand() {
			s.command(update.Message)
		} else if update.Message.Document != nil {
			s.document(update.Message)
		}
	}
}

func (s *TransferService) command(message *tgbotapi.Message) {
	msg := tgbotapi.NewMessage(message.Chat.ID, "")
	msg.ParseMode = "html"
	switch message.Command() {
	case "start":
		msg.Text = "Пожалуйста используйте команду /email для того чтобы установить Email для отправки файлов."
	case "email":
		email := strings.Trim(message.Text[6:len(message.Text)], " ")
		_, err := mail.ParseAddress(email)
		if err == nil {
			stmt, err := s.db.Prepare("INSERT INTO user('id', 'email') VALUES(?, ?) ON CONFLICT(id) DO UPDATE SET email=excluded.email;")
			if err != nil {
				log.Fatal(err)
				return
			}
			defer stmt.Close()
			_, err = stmt.Exec(message.From.ID, email)
			if err == nil {
				msg.Text = "Ваш Email установлен. \n\nПримечание: для Kindle необходимо указать email бота book.transfer.bot@gmail.com в качестве разрешенного в разделе: <b>Manage Your Content & Devices > Preferences > Personal Document Settings</b>"
			} else {
				msg.Text = "Не удалось установть Email."
			}
		} else {
			msg.Text = "Значение «" + email + "» не является правильным email адресом."
		}
	}
	if msg.Text != "" {
		if _, err := s.Bot.Send(msg); err != nil {
			log.Fatal(err)
		}
	}
}

func (s *TransferService) document(message *tgbotapi.Message) {
	var email string
	err := s.db.QueryRow("SELECT email FROM user WHERE id = ? LIMIT 1;", message.From.ID).Scan(&email)
	if err != nil {
		msg := tgbotapi.NewMessage(message.Chat.ID, "У вас не установлен Email. Пожалуйста используйте команду /email и повторите попытку.")
		if _, err := s.Bot.Send(msg); err != nil {
			log.Fatal(err)
		}
	} else {
		ext := strings.ToLower(filepath.Ext(message.Document.FileName))
		if !s.allowExtensions[ext] {
			extensions := strings.Join(strings.Split(os.Getenv("ALLOW_EXTENSION"), ";"), ", ")
			msg := tgbotapi.NewMessage(message.Chat.ID, "Файл содержит не поддерживаемый формат. \nРазрешенные форматы: <b>"+extensions+"</b>")
			msg.ParseMode = "html"
			msg.ReplyToMessageID = message.MessageID
			if _, err := s.Bot.Send(msg); err != nil {
				log.Fatal(err)
			}
			return
		}

		file, err := s.Bot.GetFile(tgbotapi.FileConfig{FileID: message.Document.FileID})
		if err != nil {
			log.Fatal(err)
			return
		}

		path, _ := os.Getwd()
		newFilename := message.Caption + ext
		directory := fmt.Sprintf("%s/upload/%s/", path, message.Document.FileUniqueID)
		if _, err := os.Stat(directory); errors.Is(err, os.ErrNotExist) {
			if os.Mkdir(directory, os.ModePerm) != nil {
				return
			}
		}

		localFilepath := fmt.Sprintf("%s/%s", directory, newFilename)
		if _, err := os.Stat(localFilepath); errors.Is(err, os.ErrNotExist) {
			var (
				out  *os.File
				resp *http.Response
			)
			out, err = os.Create(localFilepath)
			defer out.Close()
			if err != nil {
				log.Fatal(err)
				return
			}
			link := file.Link(s.Bot.Token)
			resp, err = http.Get(link)
			if err != nil {
				log.Fatal(err)
				return
			}
			defer resp.Body.Close()
			_, err = io.Copy(out, resp.Body)
			if err != nil {
				log.Fatal(err)
				return
			}
		}

		subject := ""
		if ext != ".epub" {
			subject = "convert"
		}
		msg := tgbotapi.NewMessage(message.Chat.ID, "Файл '"+newFilename+"' успешно отправлен.")
		msg.ReplyToMessageID = message.MessageID
		if config.SendEmail(email, subject, "", map[string]string{newFilename: localFilepath}) != nil {
			msg.Text = "Не удалось отправить файл."
		}
		if _, err := s.Bot.Send(msg); err != nil {
			log.Fatal(err)
		}
	}
}
