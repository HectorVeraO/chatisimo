package capitime

import (
	"context"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/HectorVeraO/chatisimo/internal/platform/llm"
)

const (
	mediaFetchTimeout   = 5 * time.Second
	maxStickerNameRunes = 64
)

// Values of the "media" field of the decision log.
const (
	mediaNone  = "none"
	mediaImage = "image"
	mediaAudio = "audio"
	mediaBoth  = "both"
)

// messageContent is everything of one message the model reads: the sanitized text, the names of
// the stickers whose image was downloaded, and the downloaded media.
type messageContent struct {
	text         string
	stickerNames []string
	images       []llm.Image
	audio        *llm.Audio
}

func (c messageContent) isEmpty() bool {
	return c.text == "" && len(c.images) == 0 && c.audio == nil
}

func (c messageContent) mediaKind() string {
	return mediaKindOf(len(c.images) > 0, c.audio != nil)
}

func mediaKindOf(hasImage, hasAudio bool) string {
	switch {
	case hasImage && hasAudio:
		return mediaBoth
	case hasImage:
		return mediaImage
	case hasAudio:
		return mediaAudio
	default:
		return mediaNone
	}
}

// normalizeImageMIME maps an announced content type to one of the image types the model accepts.
// It reports false for anything else, such as Lottie stickers or videos.
func normalizeImageMIME(raw string) (string, bool) {
	base, _, _ := strings.Cut(raw, ";")
	mime := strings.ToLower(strings.TrimSpace(base))

	switch mime {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return mime, true
	default:
		return "", false
	}
}

// usableImageRefs keeps the references that can be read: HTTPS Discord CDN address, a supported
// image type and a known size within the limit. It returns nil when none qualifies or when more
// than llm.MaxImages qualify, so an oversized message never costs an image download.
func usableImageRefs(refs []ImageRef) []ImageRef {
	var usable []ImageRef

	for _, ref := range refs {
		mime, isSupportedType := normalizeImageMIME(ref.MIMEType)
		isWithinSize := ref.Size >= 0 && ref.Size <= llm.MaxImageBytes
		if !isSupportedType || !isWithinSize || !isDiscordImageURL(ref.URL) {
			continue
		}

		ref.MIMEType = mime
		usable = append(usable, ref)
	}

	if len(usable) > llm.MaxImages {
		return nil
	}

	return usable
}

// usableAudioRefs keeps the references that can be read: HTTPS Discord CDN address, a supported audio
// type, a known size within llm.MaxAudioBytes (zero means unknown) and a known duration within
// llm.MaxAudioSeconds (zero means unknown). Each returned reference carries the normalized MIME type.
func usableAudioRefs(refs []AudioRef) []AudioRef {
	var usable []AudioRef

	for _, ref := range refs {
		mime, isSupportedType := llm.NormalizeAudioMIME(ref.MIMEType)
		isWithinSize := ref.Size >= 0 && ref.Size <= llm.MaxAudioBytes
		isFiniteDuration := !math.IsNaN(ref.DurationSeconds) && !math.IsInf(ref.DurationSeconds, 0)
		isWithinDuration := isFiniteDuration && ref.DurationSeconds >= 0 && ref.DurationSeconds <= llm.MaxAudioSeconds
		if !isSupportedType || !isWithinSize || !isWithinDuration || !isDiscordImageURL(ref.URL) {
			continue
		}

		ref.MIMEType = mime
		usable = append(usable, ref)
	}

	return usable
}

// stickerHint returns the sanitized, length-capped name of a sticker, or "" for an attachment.
func stickerHint(ref ImageRef) string {
	name := sanitizeInputText(ref.Name)
	if utf8.RuneCountInString(name) > maxStickerNameRunes {
		name = string([]rune(name)[:maxStickerNameRunes])
	}

	return name
}

// downloadContent downloads imageRefs and the first of audioRefs and returns them with text. A
// failed download drops that item only. The references must come from usableImageRefs and
// usableAudioRefs; at most one audio per message is read.
func downloadContent(
	ctx context.Context,
	fetcher ImageFetcher,
	text string,
	imageRefs []ImageRef,
	audioRefs []AudioRef,
) messageContent {
	content := messageContent{text: text}
	if fetcher == nil {
		return content
	}

	for _, ref := range imageRefs {
		data, ok := fetchMedia(ctx, fetcher, ref.URL, llm.MaxImageBytes)
		if !ok {
			continue
		}

		content.images = append(content.images, llm.Image{MIMEType: ref.MIMEType, Data: data})
		if name := stickerHint(ref); name != "" {
			content.stickerNames = append(content.stickerNames, name)
		}
	}

	if len(audioRefs) > 0 {
		ref := audioRefs[0]
		data, ok := fetchMedia(ctx, fetcher, ref.URL, llm.MaxAudioBytes)
		if ok {
			content.audio = &llm.Audio{MIMEType: ref.MIMEType, Data: data}
		}
	}

	return content
}

func fetchMedia(ctx context.Context, fetcher ImageFetcher, rawURL string, maxBytes int) ([]byte, bool) {
	fetchCtx, cancel := context.WithTimeout(ctx, mediaFetchTimeout)
	defer cancel()

	data, err := fetcher.Fetch(fetchCtx, rawURL, maxBytes)
	hasData := err == nil && len(data) > 0

	return data, hasData
}
