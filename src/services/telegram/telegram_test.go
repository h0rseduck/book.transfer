package telegram

import (
	"testing"

	"github.com/go-telegram/bot/models"
)

func TestParseCommand(t *testing.T) {
	tests := []struct {
		name      string
		text      string
		command   string
		arguments string
	}{
		{name: "start", text: "/start", command: "start"},
		{name: "email", text: "/email reader@example.com", command: "email", arguments: "reader@example.com"},
		{name: "group mention", text: "/email@book_transfer_bot reader@example.com", command: "email", arguments: "reader@example.com"},
		{name: "ordinary message", text: "hello"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command, arguments := parseCommand(test.text)
			if command != test.command || arguments != test.arguments {
				t.Fatalf("parseCommand(%q) = (%q, %q), want (%q, %q)", test.text, command, arguments, test.command, test.arguments)
			}
		})
	}
}

func TestTransferReactionAdded(t *testing.T) {
	bookReaction := models.ReactionType{
		Type: models.ReactionTypeTypeEmoji,
		ReactionTypeEmoji: &models.ReactionTypeEmoji{
			Emoji: transferReactionEmoji,
		},
	}
	otherReaction := models.ReactionType{
		Type: models.ReactionTypeTypeEmoji,
		ReactionTypeEmoji: &models.ReactionTypeEmoji{
			Emoji: "👎",
		},
	}

	if !transferReactionAdded(nil, []models.ReactionType{bookReaction}) {
		t.Fatal("adding the book reaction must trigger a transfer")
	}
	if transferReactionAdded([]models.ReactionType{bookReaction}, nil) {
		t.Fatal("removing the book reaction must not trigger a transfer")
	}
	if transferReactionAdded([]models.ReactionType{bookReaction}, []models.ReactionType{bookReaction}) {
		t.Fatal("an unchanged book reaction must not trigger a transfer")
	}
	if transferReactionAdded(nil, []models.ReactionType{otherReaction}) {
		t.Fatal("an unrelated reaction must not trigger a transfer")
	}
}

func TestDocumentFilename(t *testing.T) {
	document := &models.Document{
		FileUniqueID: "unique-id",
		FileName:     "original.epub",
	}

	if got := documentFilename(document, "My Book"); got != "My Book.epub" {
		t.Fatalf("caption filename = %q, want %q", got, "My Book.epub")
	}
	if got := documentFilename(document, ""); got != "original.epub" {
		t.Fatalf("fallback filename = %q, want %q", got, "original.epub")
	}
}

func TestAllowedExtensionsAreCaseInsensitive(t *testing.T) {
	service := &TransferService{allowExtensions: parseAllowedExtensions(".epub;.PDF")}
	if !service.isAllowedDocument(&models.Document{FileName: "book.EPUB"}) {
		t.Fatal("expected uppercase EPUB extension to be allowed")
	}
	if !service.isAllowedDocument(&models.Document{FileName: "book.pdf"}) {
		t.Fatal("expected normalized PDF extension to be allowed")
	}
	if service.isAllowedDocument(&models.Document{FileName: "book.exe"}) {
		t.Fatal("unexpected extension must be rejected")
	}
}
