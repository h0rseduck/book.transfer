package telegram

import (
	"book.transfer/src/config"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/mail"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	telegrambot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

const (
	transferReactionEmoji   = "👍"
	processingReactionEmoji = "👀"
	successReactionEmoji    = "✅"
	failureReactionEmoji    = "❌"
)

type TransferService struct {
	Bot             *telegrambot.Bot
	db              *sql.DB
	allowUserIDs    map[int64]bool
	allowExtensions map[string]bool
	downloadClient  *http.Client
}

type storedDocument struct {
	FileID       string
	FileUniqueID string
	FileName     string
	Caption      string
}

func NewTransferService() (*TransferService, error) {
	service := &TransferService{
		db:              config.GetDB(),
		allowUserIDs:    parseAllowedUserIDs(os.Getenv("ALLOW_IDS")),
		allowExtensions: parseAllowedExtensions(os.Getenv("ALLOW_EXTENSION")),
		downloadClient:  &http.Client{Timeout: 2 * time.Minute},
	}

	bot, err := telegrambot.New(
		os.Getenv("TELEGRAM_TOKEN"),
		telegrambot.WithDefaultHandler(service.handleUpdate),
		telegrambot.WithNotAsyncHandlers(),
		telegrambot.WithErrorsHandler(func(err error) {
			log.Printf("telegram update error: %v", err)
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("create Telegram bot: %w", err)
	}

	service.Bot = bot
	return service, nil
}

func parseAllowedUserIDs(raw string) map[int64]bool {
	result := make(map[int64]bool)
	for _, value := range strings.Split(raw, ";") {
		id, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err == nil {
			result[id] = true
		}
	}
	return result
}

func parseAllowedExtensions(raw string) map[string]bool {
	result := make(map[string]bool)
	for _, value := range strings.Split(raw, ";") {
		extension := strings.ToLower(strings.TrimSpace(value))
		if extension != "" {
			result[extension] = true
		}
	}
	return result
}

func (s *TransferService) ListenForWebhook(ctx context.Context) error {
	webhookPath := "/" + strings.TrimLeft(strings.TrimSpace(os.Getenv("TELEGRAM_WEBHOOK")), "/")
	if webhookPath == "/" {
		return errors.New("TELEGRAM_WEBHOOK is empty")
	}

	webhookURL := strings.TrimRight(strings.TrimSpace(os.Getenv("DOMAIN")), "/")
	if webhookURL == "" {
		return errors.New("DOMAIN is empty")
	}
	if !strings.HasPrefix(webhookURL, "https://") && !strings.HasPrefix(webhookURL, "http://") {
		webhookURL = "https://" + webhookURL
	}
	webhookURL += webhookPath

	mux := http.NewServeMux()
	mux.Handle(webhookPath, s.Bot.WebhookHandler())
	server := &http.Server{
		Addr:              "0.0.0.0:3000",
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		err := server.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
			return
		}
		serverErrors <- nil
	}()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown webhook server: %v", err)
		}
	}()

	go s.pruneServiceData(ctx)

	_, err := s.Bot.SetWebhook(ctx, &telegrambot.SetWebhookParams{
		URL: webhookURL,
		AllowedUpdates: []string{
			models.AllowedUpdateMessage,
			models.AllowedUpdateMessageReaction,
		},
	})
	if err != nil {
		return fmt.Errorf("configure Telegram webhook: %w", err)
	}
	log.Printf("Telegram webhook configured; updates=message,message_reaction")

	botDone := make(chan struct{})
	go func() {
		s.Bot.StartWebhook(ctx)
		close(botDone)
	}()

	select {
	case err := <-serverErrors:
		return err
	case <-botDone:
		return nil
	}
}

func (s *TransferService) pruneServiceData(ctx context.Context) {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := config.PruneServiceData(time.Now().Add(-config.ServiceDataRetention)); err != nil {
				log.Printf("prune Telegram service data: %v", err)
			}
		}
	}
}

func (s *TransferService) handleUpdate(ctx context.Context, _ *telegrambot.Bot, update *models.Update) {
	if update == nil {
		return
	}
	if update.MessageReaction != nil {
		s.handleReaction(ctx, update.MessageReaction)
		return
	}
	if update.Message != nil {
		s.handleMessage(ctx, update.Message)
	}
}

func (s *TransferService) handleMessage(ctx context.Context, message *models.Message) {
	if message.From == nil {
		return
	}

	if message.From.IsBot {
		if message.From.ID != s.Bot.ID() && message.Document != nil {
			s.rememberBotDocument(ctx, message)
		}
		return
	}

	if !s.allowUserIDs[message.From.ID] {
		return
	}

	command, arguments := parseCommand(message.Text)
	if command != "" {
		s.command(ctx, message, command, arguments)
		return
	}
	if message.Document != nil {
		s.document(ctx, message)
	}
}

func parseCommand(text string) (string, string) {
	fields := strings.Fields(text)
	if len(fields) == 0 || !strings.HasPrefix(fields[0], "/") {
		return "", ""
	}

	command := strings.TrimPrefix(fields[0], "/")
	if at := strings.Index(command, "@"); at >= 0 {
		command = command[:at]
	}
	arguments := strings.TrimSpace(strings.TrimPrefix(text, fields[0]))
	return strings.ToLower(command), arguments
}

func (s *TransferService) command(ctx context.Context, message *models.Message, command, arguments string) {
	text := ""
	parseMode := models.ParseMode("")

	switch command {
	case "start":
		text = "Пожалуйста используйте команду /email для того чтобы установить Email для отправки файлов."
	case "email":
		emailAddress := strings.TrimSpace(arguments)
		if _, err := mail.ParseAddress(emailAddress); err != nil {
			text = "Значение «" + emailAddress + "» не является правильным email адресом."
			break
		}

		_, err := s.db.Exec(
			"INSERT INTO user('id', 'email') VALUES(?, ?) ON CONFLICT(id) DO UPDATE SET email=excluded.email;",
			message.From.ID,
			emailAddress,
		)
		if err != nil {
			log.Printf("save email for user %d: %v", message.From.ID, err)
			text = "Не удалось установить Email."
			break
		}

		text = "Ваш Email установлен. \n\nПримечание: для Kindle необходимо указать email бота book.transfer.bot@gmail.com в качестве разрешенного в разделе: <b>Manage Your Content & Devices > Preferences > Personal Document Settings</b>"
		parseMode = models.ParseModeHTML
	}

	if text != "" {
		s.sendReply(ctx, message.Chat.ID, message.ID, text, parseMode)
	}
}

func (s *TransferService) document(ctx context.Context, message *models.Message) {
	emailAddress, err := s.userEmail(message.From.ID)
	if errors.Is(err, sql.ErrNoRows) {
		s.sendReply(ctx, message.Chat.ID, message.ID, "У вас не установлен Email. Пожалуйста используйте команду /email и повторите попытку.", "")
		return
	}
	if err != nil {
		log.Printf("load email for user %d: %v", message.From.ID, err)
		return
	}

	if !s.isAllowedDocument(message.Document) {
		extensions := strings.Join(strings.Split(os.Getenv("ALLOW_EXTENSION"), ";"), ", ")
		text := "Файл содержит не поддерживаемый формат. \nРазрешенные форматы: <b>" + extensions + "</b>"
		s.sendReply(ctx, message.Chat.ID, message.ID, text, models.ParseModeHTML)
		return
	}

	filename, err := s.sendDocumentToEmail(ctx, message.Document, message.Caption, emailAddress)
	text := "Файл '" + filename + "' успешно отправлен."
	if err != nil {
		log.Printf("send user document to email: %v", err)
		text = "Не удалось отправить файл."
	} else {
		log.Printf("direct document transfer completed chat_id=%d message_id=%d user_id=%d", message.Chat.ID, message.ID, message.From.ID)
	}
	s.sendReply(ctx, message.Chat.ID, message.ID, text, "")
}

func (s *TransferService) rememberBotDocument(ctx context.Context, message *models.Message) {
	if !s.isAllowedDocument(message.Document) {
		return
	}

	_, err := s.db.Exec(
		`INSERT INTO bot_document(chat_id, message_id, file_id, file_unique_id, file_name, caption, source_bot_id)
		 VALUES(?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(chat_id, message_id) DO UPDATE SET
			file_id=excluded.file_id,
			file_unique_id=excluded.file_unique_id,
			file_name=excluded.file_name,
			caption=excluded.caption,
			source_bot_id=excluded.source_bot_id,
			created_at=strftime('%s', 'now')`,
		message.Chat.ID,
		message.ID,
		message.Document.FileID,
		message.Document.FileUniqueID,
		message.Document.FileName,
		message.Caption,
		message.From.ID,
	)
	if err != nil {
		log.Printf("remember bot document %d/%d: %v", message.Chat.ID, message.ID, err)
		return
	}
	log.Printf("bot document stored chat_id=%d message_id=%d source_bot_id=%d extension=%q", message.Chat.ID, message.ID, message.From.ID, strings.ToLower(filepath.Ext(message.Document.FileName)))

	if s.setStatusReaction(ctx, message.Chat.ID, message.ID, transferReactionEmoji) {
		log.Printf("trigger reaction added chat_id=%d message_id=%d", message.Chat.ID, message.ID)
	}
}

func (s *TransferService) handleReaction(ctx context.Context, reaction *models.MessageReactionUpdated) {
	if reaction.User == nil || reaction.User.IsBot || !s.allowUserIDs[reaction.User.ID] {
		return
	}
	if !transferReactionAdded(reaction.OldReaction, reaction.NewReaction) {
		return
	}
	log.Printf("transfer reaction received chat_id=%d message_id=%d user_id=%d", reaction.Chat.ID, reaction.MessageID, reaction.User.ID)

	document, err := s.storedBotDocument(reaction.Chat.ID, reaction.MessageID)
	if errors.Is(err, sql.ErrNoRows) {
		log.Printf("reaction has no stored bot document chat_id=%d message_id=%d user_id=%d", reaction.Chat.ID, reaction.MessageID, reaction.User.ID)
		s.setStatusReaction(ctx, reaction.Chat.ID, reaction.MessageID, failureReactionEmoji)
		s.sendPrivateStatus(ctx, reaction.User.ID, "❌ Не удалось отправить файл: бот не получил исходное сообщение. Попросите второго бота прислать новый файл и поставьте на нём 👍.")
		return
	}
	if err != nil {
		log.Printf("load bot document %d/%d: %v", reaction.Chat.ID, reaction.MessageID, err)
		s.setStatusReaction(ctx, reaction.Chat.ID, reaction.MessageID, failureReactionEmoji)
		s.sendPrivateStatus(ctx, reaction.User.ID, "❌ Не удалось прочитать сведения о файле. Попробуйте убрать и снова поставить 👍 немного позже.")
		return
	}

	emailAddress, err := s.userEmail(reaction.User.ID)
	if errors.Is(err, sql.ErrNoRows) {
		log.Printf("reacting user has no registered email user_id=%d", reaction.User.ID)
		s.setStatusReaction(ctx, reaction.Chat.ID, reaction.MessageID, failureReactionEmoji)
		s.sendPrivateStatus(ctx, reaction.User.ID, "❌ Email не зарегистрирован. Отправьте мне в личном чате команду /email адрес@example.com, затем повторите 👍 в группе.")
		return
	}
	if err != nil {
		log.Printf("load email for reacting user %d: %v", reaction.User.ID, err)
		s.setStatusReaction(ctx, reaction.Chat.ID, reaction.MessageID, failureReactionEmoji)
		s.sendPrivateStatus(ctx, reaction.User.ID, "❌ Не удалось прочитать зарегистрированный Email. Попробуйте повторить отправку немного позже.")
		return
	}

	claimed, err := s.claimTransfer(reaction.Chat.ID, reaction.MessageID, reaction.User.ID)
	if err != nil {
		log.Printf("claim document transfer: %v", err)
		s.setStatusReaction(ctx, reaction.Chat.ID, reaction.MessageID, failureReactionEmoji)
		s.sendPrivateStatus(ctx, reaction.User.ID, "❌ Не удалось запустить отправку. Попробуйте убрать и снова поставить 👍 немного позже.")
		return
	}
	if !claimed {
		log.Printf("duplicate reaction transfer ignored chat_id=%d message_id=%d user_id=%d", reaction.Chat.ID, reaction.MessageID, reaction.User.ID)
		return
	}
	log.Printf("reaction transfer started chat_id=%d message_id=%d user_id=%d", reaction.Chat.ID, reaction.MessageID, reaction.User.ID)
	s.setStatusReaction(ctx, reaction.Chat.ID, reaction.MessageID, processingReactionEmoji)

	telegramDocument := &models.Document{
		FileID:       document.FileID,
		FileUniqueID: document.FileUniqueID,
		FileName:     document.FileName,
	}
	_, err = s.sendDocumentToEmail(ctx, telegramDocument, document.Caption, emailAddress)
	if err != nil {
		s.releaseTransfer(reaction.Chat.ID, reaction.MessageID, reaction.User.ID)
		log.Printf("send reacted document to email: %v", err)
		s.setStatusReaction(ctx, reaction.Chat.ID, reaction.MessageID, failureReactionEmoji)
		s.sendPrivateStatus(ctx, reaction.User.ID, "❌ Не удалось отправить файл на зарегистрированный Email. Уберите и снова поставьте 👍, чтобы повторить попытку.")
		return
	}

	if !s.setStatusReaction(ctx, reaction.Chat.ID, reaction.MessageID, successReactionEmoji) {
		s.sendPrivateStatus(ctx, reaction.User.ID, "✅ Файл отправлен на зарегистрированный Email, но обновить статусную реакцию в группе не удалось.")
	}
	log.Printf("reaction transfer completed chat_id=%d message_id=%d user_id=%d", reaction.Chat.ID, reaction.MessageID, reaction.User.ID)
}

func (s *TransferService) setStatusReaction(ctx context.Context, chatID int64, messageID int, emoji string) bool {
	_, err := s.Bot.SetMessageReaction(ctx, &telegrambot.SetMessageReactionParams{
		ChatID:    chatID,
		MessageID: messageID,
		Reaction: []models.ReactionType{{
			Type: models.ReactionTypeTypeEmoji,
			ReactionTypeEmoji: &models.ReactionTypeEmoji{
				Emoji: emoji,
			},
		}},
	})
	if err != nil {
		log.Printf("set Telegram status reaction emoji=%s chat_id=%d message_id=%d: %v", emoji, chatID, messageID, err)
		return false
	}
	return true
}

func (s *TransferService) sendPrivateStatus(ctx context.Context, userID int64, text string) {
	if _, err := s.Bot.SendMessage(ctx, &telegrambot.SendMessageParams{
		ChatID: userID,
		Text:   text,
	}); err != nil {
		log.Printf("send private transfer status user_id=%d: %v", userID, err)
	}
}

func transferReactionAdded(oldReactions, newReactions []models.ReactionType) bool {
	return !containsReaction(oldReactions, transferReactionEmoji) && containsReaction(newReactions, transferReactionEmoji)
}

func containsReaction(reactions []models.ReactionType, emoji string) bool {
	for _, reaction := range reactions {
		if reaction.Type == models.ReactionTypeTypeEmoji &&
			reaction.ReactionTypeEmoji != nil &&
			reaction.ReactionTypeEmoji.Emoji == emoji {
			return true
		}
	}
	return false
}

func (s *TransferService) isAllowedDocument(document *models.Document) bool {
	if document == nil {
		return false
	}
	extension := strings.ToLower(filepath.Ext(document.FileName))
	return s.allowExtensions[extension]
}

func (s *TransferService) userEmail(userID int64) (string, error) {
	var emailAddress string
	err := s.db.QueryRow("SELECT email FROM user WHERE id = ? LIMIT 1", userID).Scan(&emailAddress)
	return emailAddress, err
}

func (s *TransferService) storedBotDocument(chatID int64, messageID int) (storedDocument, error) {
	document := storedDocument{}
	err := s.db.QueryRow(
		`SELECT file_id, file_unique_id, file_name, caption
		 FROM bot_document
		 WHERE chat_id = ? AND message_id = ?
		 LIMIT 1`,
		chatID,
		messageID,
	).Scan(&document.FileID, &document.FileUniqueID, &document.FileName, &document.Caption)
	return document, err
}

func (s *TransferService) claimTransfer(chatID int64, messageID int, userID int64) (bool, error) {
	result, err := s.db.Exec(
		"INSERT OR IGNORE INTO document_transfer(chat_id, message_id, user_id) VALUES(?, ?, ?)",
		chatID,
		messageID,
		userID,
	)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func (s *TransferService) releaseTransfer(chatID int64, messageID int, userID int64) {
	if _, err := s.db.Exec(
		"DELETE FROM document_transfer WHERE chat_id = ? AND message_id = ? AND user_id = ?",
		chatID,
		messageID,
		userID,
	); err != nil {
		log.Printf("release document transfer: %v", err)
	}
}

func (s *TransferService) sendDocumentToEmail(ctx context.Context, document *models.Document, caption, emailAddress string) (string, error) {
	file, err := s.Bot.GetFile(ctx, &telegrambot.GetFileParams{FileID: document.FileID})
	if err != nil {
		return "", fmt.Errorf("get Telegram file: %w", err)
	}

	filename := documentFilename(document, caption)
	workingDirectory, err := os.Getwd()
	if err != nil {
		return filename, fmt.Errorf("get working directory: %w", err)
	}
	uploadDirectory := filepath.Join(workingDirectory, "upload")
	if err := os.MkdirAll(uploadDirectory, os.ModePerm); err != nil {
		return filename, fmt.Errorf("create upload directory: %w", err)
	}
	directory, err := os.MkdirTemp(uploadDirectory, "transfer-")
	if err != nil {
		return filename, fmt.Errorf("create temporary transfer directory: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(directory); err != nil {
			log.Printf("remove temporary transfer directory: %v", err)
		}
	}()

	localFilepath := filepath.Join(directory, filename)
	if err := s.downloadFile(ctx, s.Bot.FileDownloadLink(file), localFilepath); err != nil {
		return filename, err
	}

	subject := ""
	if strings.ToLower(filepath.Ext(document.FileName)) != ".epub" {
		subject = "convert"
	}
	if err := config.SendEmail(emailAddress, subject, "", map[string]string{filename: localFilepath}); err != nil {
		return filename, fmt.Errorf("send email: %w", err)
	}
	return filename, nil
}

func documentFilename(document *models.Document, caption string) string {
	extension := strings.ToLower(filepath.Ext(document.FileName))
	base := strings.TrimSpace(caption)
	if base == "" {
		filename := filepath.Base(document.FileName)
		if filename != "." && filename != "" {
			return filename
		}
		base = document.FileUniqueID
	}
	return filepath.Base(base) + extension
}

func (s *TransferService) downloadFile(ctx context.Context, link, destination string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return fmt.Errorf("create file request: %w", err)
	}
	response, err := s.downloadClient.Do(request)
	if err != nil {
		return fmt.Errorf("download Telegram file: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("download Telegram file: unexpected HTTP status %s", response.Status)
	}

	temporaryFile, err := os.CreateTemp(filepath.Dir(destination), ".telegram-download-*")
	if err != nil {
		return fmt.Errorf("create local file: %w", err)
	}
	temporaryPath := temporaryFile.Name()
	_, copyErr := io.Copy(temporaryFile, response.Body)
	closeErr := temporaryFile.Close()
	if copyErr != nil {
		_ = os.Remove(temporaryPath)
		return fmt.Errorf("save Telegram file: %w", copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(temporaryPath)
		return fmt.Errorf("close Telegram file: %w", closeErr)
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		_ = os.Remove(temporaryPath)
		return fmt.Errorf("finish Telegram file download: %w", err)
	}
	return nil
}

func (s *TransferService) sendReply(ctx context.Context, chatID int64, messageID int, text string, parseMode models.ParseMode) {
	_, err := s.Bot.SendMessage(ctx, &telegrambot.SendMessageParams{
		ChatID:    chatID,
		Text:      text,
		ParseMode: parseMode,
		ReplyParameters: &models.ReplyParameters{
			MessageID:                messageID,
			AllowSendingWithoutReply: true,
		},
	})
	if err != nil {
		log.Printf("send Telegram reply: %v", err)
	}
}
