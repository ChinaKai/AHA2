package mail

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"mime"
	"mime/multipart"
	"net/mail"
	"net/textproto"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// OutgoingAttachment is a direct MIME attachment. Link-mode attachments are
// not added as MIME parts; the worker replaces their body placeholders with a
// controlled download URL.
type OutgoingAttachment struct {
	FileName  string
	MediaType string
	ContentID string
	Content   []byte
}

// Outgoing is the fully resolved message the worker hands to the SMTP sender.
// It contains body text and attachment bytes in memory for the duration of one
// attempt only.
type Outgoing struct {
	FromAddress string
	FromName    string
	To          []string
	CC          []string
	BCC         []string
	Subject     string
	BodyText    string
	BodyHTML    string
	MessageID   string
	Attachments []OutgoingAttachment
	Now         time.Time
}

func headerSafe(value string) bool {
	return !strings.ContainsAny(value, "\r\n")
}

func headerAddress(address, name string) (string, error) {
	if !headerSafe(address) || !headerSafe(name) {
		return "", errorf(domain.MailErrRecipientInvalid, "mail header contains a line break")
	}
	if _, err := NormalizeRecipient(address); err != nil {
		return "", err
	}
	if strings.TrimSpace(name) == "" {
		return address, nil
	}
	return (&mail.Address{Name: name, Address: address}).String(), nil
}

func encodeHeader(value string) (string, error) {
	if !headerSafe(value) {
		return "", errorf(domain.MailErrTemplateInvalid, "mail header contains a line break")
	}
	if value == "" {
		return "", nil
	}
	if isASCII(value) {
		if !strings.Contains(value, " ") && len(value) > 900 {
			// A single unbroken token cannot be folded at a space; encoded words
			// keep the RFC 5322 hard line limit. (Ordinary subjects fold below.)
			return foldEncodedWords(value), nil
		}
		return foldASCII(value), nil
	}
	return foldEncodedWords(value), nil
}

// foldASCII folds a long ASCII header at existing spaces. Folding whitespace
// is semantically one space, so the decoded value is unchanged.
func foldASCII(value string) string {
	const maxLine = 78
	if len(value) <= maxLine {
		return value
	}
	words := strings.Split(value, " ")
	var builder strings.Builder
	builder.Grow(len(value) + len(value)/maxLine*2)
	lineLength := 0
	for index, word := range words {
		added := len(word)
		if index > 0 {
			added++
		}
		if index > 0 {
			if lineLength > 0 && lineLength+added > maxLine {
				builder.WriteString("\r\n ")
				lineLength = 1
			} else {
				builder.WriteByte(' ')
				lineLength++
			}
		}
		builder.WriteString(word)
		lineLength += len(word)
	}
	return builder.String()
}

// foldEncodedWords emits adjacent RFC 2047 encoded words separated by folded
// whitespace. Each encoded word stays well below the 75-octet limit, and
// decoders concatenate adjacent words back to the exact original value.
func foldEncodedWords(value string) string {
	words := make([]string, 0, len(value)/36+1)
	var chunk strings.Builder
	chunkBytes := 0
	for _, r := range value {
		size := utf8.RuneLen(r)
		if size < 0 {
			size = 1
		}
		if chunkBytes+size > 45 && chunk.Len() > 0 {
			words = append(words, mime.BEncoding.Encode("UTF-8", chunk.String()))
			chunk.Reset()
			chunkBytes = 0
		}
		chunk.WriteRune(r)
		chunkBytes += size
	}
	if chunk.Len() > 0 {
		words = append(words, mime.BEncoding.Encode("UTF-8", chunk.String()))
	}
	return strings.Join(words, "\r\n ")
}

func isASCII(value string) bool {
	for _, r := range value {
		if r > 0x7f {
			return false
		}
	}
	return true
}

// formatAttachmentMediaType parses the media type first, then re-formats the
// bare type with its parameters plus the filename. Passing a value that already
// contains parameters directly to FormatMediaType returns an empty string,
// producing an empty Content-Type header and losing the name.
func formatAttachmentMediaType(value, filename string) string {
	value = strings.TrimSpace(value)
	parsed, params, err := mime.ParseMediaType(value)
	if err != nil || strings.TrimSpace(parsed) == "" || !headerSafe(parsed) {
		parsed = "application/octet-stream"
		params = nil
	}
	if params == nil {
		params = map[string]string{}
	}
	params["name"] = filename
	return mime.FormatMediaType(parsed, params)
}

func base64Lines(data []byte) []byte {
	encoded := base64.StdEncoding.EncodeToString(data)
	var builder strings.Builder
	builder.Grow(len(encoded) + len(encoded)/76*2 + 2)
	for len(encoded) > 76 {
		builder.WriteString(encoded[:76])
		builder.WriteString("\r\n")
		encoded = encoded[76:]
	}
	builder.WriteString(encoded)
	builder.WriteString("\r\n")
	return []byte(builder.String())
}

func writeBase64Part(writer *multipart.Writer, contentType string, content []byte) error {
	header := textproto.MIMEHeader{}
	header.Set("Content-Type", contentType)
	header.Set("Content-Transfer-Encoding", "base64")
	part, err := writer.CreatePart(header)
	if err != nil {
		return err
	}
	_, err = part.Write(base64Lines(content))
	return err
}

// buildAlternative returns a complete multipart/alternative body. The caller
// embeds it either as the whole message or as the first part of multipart/mixed.
func buildAlternative(outgoing Outgoing) ([]byte, string, error) {
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	if err := writeBase64Part(writer, "text/plain; charset=utf-8", []byte(outgoing.BodyText)); err != nil {
		return nil, "", err
	}
	if err := writeBase64Part(writer, "text/html; charset=utf-8", []byte(outgoing.BodyHTML)); err != nil {
		return nil, "", err
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return buffer.Bytes(), writer.Boundary(), nil
}

// BuildMessage renders a CRLF RFC 5322 message with multipart/mixed and
// multipart/alternative structure. It never includes BCC in headers.
func BuildMessage(outgoing Outgoing) ([]byte, error) {
	from, err := headerAddress(outgoing.FromAddress, outgoing.FromName)
	if err != nil {
		return nil, err
	}
	subject, err := encodeHeader(outgoing.Subject)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(outgoing.MessageID) == "" || !headerSafe(outgoing.MessageID) ||
		!strings.HasPrefix(outgoing.MessageID, "<") || !strings.HasSuffix(outgoing.MessageID, ">") {
		return nil, errorf(domain.MailErrTemplateInvalid, "invalid Message-ID")
	}
	recipients := make([]string, 0, len(outgoing.To)+len(outgoing.CC))
	for _, address := range outgoing.To {
		value, err := headerAddress(address, "")
		if err != nil {
			return nil, err
		}
		recipients = append(recipients, value)
	}
	cc := make([]string, 0, len(outgoing.CC))
	for _, address := range outgoing.CC {
		value, err := headerAddress(address, "")
		if err != nil {
			return nil, err
		}
		cc = append(cc, value)
	}
	if len(recipients) == 0 {
		return nil, errorf(domain.MailErrRecipientInvalid, "mail requires at least one recipient")
	}
	now := outgoing.Now
	if now.IsZero() {
		now = time.Now()
	}

	bodyText := outgoing.BodyText
	if bodyText == "" && outgoing.BodyHTML != "" {
		bodyText = htmlToText(outgoing.BodyHTML)
	}
	outgoing.BodyText = bodyText

	var body bytes.Buffer
	contentType := ""
	topLevelEncoding := ""
	if len(outgoing.Attachments) == 0 {
		if outgoing.BodyHTML != "" {
			alternative, boundary, err := buildAlternative(outgoing)
			if err != nil {
				return nil, err
			}
			contentType = mime.FormatMediaType("multipart/alternative", map[string]string{"boundary": boundary})
			body.Write(alternative)
		} else {
			contentType = "text/plain; charset=utf-8"
			topLevelEncoding = "base64"
			body.Write(base64Lines([]byte(bodyText)))
		}
	} else {
		mixed := multipart.NewWriter(&body)
		if outgoing.BodyHTML != "" {
			alternative, boundary, err := buildAlternative(outgoing)
			if err != nil {
				return nil, err
			}
			header := textproto.MIMEHeader{}
			header.Set("Content-Type", mime.FormatMediaType("multipart/alternative", map[string]string{"boundary": boundary}))
			part, err := mixed.CreatePart(header)
			if err != nil {
				return nil, err
			}
			if _, err := part.Write(alternative); err != nil {
				return nil, err
			}
		} else if err := writeBase64Part(mixed, "text/plain; charset=utf-8", []byte(bodyText)); err != nil {
			return nil, err
		}
		for _, attachment := range outgoing.Attachments {
			if !headerSafe(attachment.FileName) {
				return nil, errorf(domain.MailErrAttachmentNotFound, "attachment file name contains a line break")
			}
			header := textproto.MIMEHeader{}
			header.Set("Content-Type", formatAttachmentMediaType(attachment.MediaType, attachment.FileName))
			header.Set("Content-Transfer-Encoding", "base64")
			header.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{
				"filename": attachment.FileName,
			}))
			if attachment.ContentID != "" && headerSafe(attachment.ContentID) {
				header.Set("Content-ID", "<"+attachment.ContentID+">")
			}
			part, err := mixed.CreatePart(header)
			if err != nil {
				return nil, err
			}
			if _, err := part.Write(base64Lines(attachment.Content)); err != nil {
				return nil, err
			}
		}
		if err := mixed.Close(); err != nil {
			return nil, err
		}
		contentType = mime.FormatMediaType("multipart/mixed", map[string]string{"boundary": mixed.Boundary()})
	}

	var message bytes.Buffer
	writeHeader := func(name, value string) {
		fmt.Fprintf(&message, "%s: %s\r\n", name, value)
	}
	writeHeader("Date", now.Format(time.RFC1123Z))
	writeHeader("From", from)
	writeHeader("To", strings.Join(recipients, ", "))
	if len(cc) > 0 {
		writeHeader("Cc", strings.Join(cc, ", "))
	}
	writeHeader("Subject", subject)
	writeHeader("Message-ID", outgoing.MessageID)
	writeHeader("MIME-Version", "1.0")
	writeHeader("Content-Type", contentType)
	if topLevelEncoding != "" {
		writeHeader("Content-Transfer-Encoding", topLevelEncoding)
	}
	message.WriteString("\r\n")
	message.Write(body.Bytes())
	return message.Bytes(), nil
}
