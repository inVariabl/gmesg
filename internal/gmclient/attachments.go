package gmclient

import (
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"go.mau.fi/mautrix-gmessages/pkg/libgm"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
)

// UploadAttachment reads the file at path, uploads it, and returns the
// resulting MediaContent ready to embed in a SendMessageRequest. The MIME
// type is guessed from the file extension, falling back to content
// sniffing for extension-less files.
func UploadAttachment(client *libgm.Client, path string) (*gmproto.MediaContent, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	mimeType := mime.TypeByExtension(filepath.Ext(path))
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}
	media, err := client.UploadMedia(data, filepath.Base(path), mimeType)
	if err != nil {
		return nil, fmt.Errorf("upload %s: %w", path, err)
	}
	return media, nil
}

// DownloadAttachment fetches and decrypts a received attachment's raw
// bytes.
func DownloadAttachment(client *libgm.Client, media *gmproto.MediaContent) ([]byte, error) {
	if !AttachmentReady(media) {
		return nil, fmt.Errorf("%s is still being received from the phone, try again in a moment", media.GetMediaName())
	}
	data, err := client.DownloadMedia(media.GetMediaID(), media.GetDecryptionKey())
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", media.GetMediaName(), err)
	}
	return data, nil
}

// RequestFullMedia asks Google to push the complete version of an
// attachment -- including its decryption key -- through the live event
// stream. It's fire-and-forget: the RPC response carries nothing itself,
// the actual media follows a moment later as a normal message-update event
// (which the caller's existing event handling will pick up by message ID).
func RequestFullMedia(client *libgm.Client, a Attachment) error {
	if a.ActionMessageID == "" {
		return fmt.Errorf("no action message ID for %s, can't request full media", a.Media.GetMediaName())
	}
	if _, err := client.GetFullSizeImage(a.MessageID, a.ActionMessageID); err != nil {
		return fmt.Errorf("request full media for %s: %w", a.Media.GetMediaName(), err)
	}
	return nil
}

// AttachmentReady reports whether media has finished transferring from the
// phone and is safe to download. Incoming attachments arrive incrementally:
// Google relays the media ID and file name before the AES-256 decryption
// key is populated, so a media ID alone isn't enough to know a download
// will succeed.
func AttachmentReady(media *gmproto.MediaContent) bool {
	return media.GetMediaID() != "" && len(media.GetDecryptionKey()) == 32
}

// EnsureCached downloads media into cacheDir if it isn't already there and
// returns its local path, ready to hand to an external viewer or an image
// preview overlay. media must be AttachmentReady.
func EnsureCached(client *libgm.Client, cacheDir string, media *gmproto.MediaContent) (string, error) {
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return "", fmt.Errorf("create cache dir: %w", err)
	}
	path := filepath.Join(cacheDir, CacheFileName(media))
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	data, err := DownloadAttachment(client, media)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return path, nil
}

// CacheFileName derives a filesystem-safe, stable file name for caching a
// downloaded attachment: unique per media ID (so re-downloads are skipped)
// and stripped of any path components a malicious MediaName could carry.
func CacheFileName(media *gmproto.MediaContent) string {
	name := media.GetMediaName()
	if name == "" {
		name = "attachment"
	}
	id := strings.NewReplacer("/", "_", "\\", "_").Replace(media.GetMediaID())
	return id + "-" + filepath.Base(name)
}
