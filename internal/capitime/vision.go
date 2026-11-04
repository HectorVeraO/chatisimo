package capitime

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/HectorVeraO/chatisimo/internal/platform/llm"
)

const (
	imageFetchTimeout     = 5 * time.Second
	visionClassifyTimeout = 8 * time.Second
	maxStickerNameRunes   = 64
)

const visionInstructionExtra = "" +
	"El mensaje puede traer una o dos imagenes (fotos o stickers) en lugar de texto, o ademas de el. " +
	"Lee cualquier texto dibujado en la imagen (stickers o carteles como VOY, ENTRANDING, LLEGANDO, YA VOY) " +
	"y tratalo como si estuviera escrito en el mensaje. " +
	"Ignora imagenes sin texto o sin relacion con llegar o conectarse. "

const visionSystemInstruction = stage2Rules + visionInstructionExtra + stage2JSONContract

// normalizeImageMIME maps an announced content type to one of the image types the vision model
// accepts. It reports false for anything else, such as Lottie stickers or videos.
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
// than llm.MaxImages qualify, so an oversized message never costs a download or a model call.
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

// visionUserText joins the sanitized caption with a hint per sticker name. It is empty when the
// message has neither.
func visionUserText(caption string, refs []ImageRef) string {
	var lines []string

	cleanedCaption := sanitizeInputText(caption)
	if cleanedCaption != "" {
		lines = append(lines, cleanedCaption)
	}

	for _, ref := range refs {
		name := sanitizeInputText(ref.Name)
		if utf8.RuneCountInString(name) > maxStickerNameRunes {
			name = string([]rune(name)[:maxStickerNameRunes])
		}
		if name != "" {
			lines = append(lines, "sticker: "+name)
		}
	}

	return strings.Join(lines, "\n")
}

// classifyImagesWithVision downloads refs and asks the vision model whether the caption and the
// images together announce an arrival. Any fetch or model failure yields no claim. refs must
// come from usableImageRefs.
func classifyImagesWithVision(
	ctx context.Context,
	vision VisionGenerator,
	fetcher ImageFetcher,
	refs []ImageRef,
	caption string,
	now time.Time,
) ArrivalClaim {
	if vision == nil || fetcher == nil || ctx == nil || len(refs) == 0 {
		return ArrivalClaim{}
	}

	images := make([]llm.Image, 0, len(refs))
	for _, ref := range refs {
		data, ok := fetchImage(ctx, fetcher, ref.URL)
		if !ok {
			return ArrivalClaim{}
		}

		images = append(images, llm.Image{MIMEType: ref.MIMEType, Data: data})
	}

	callCtx, cancel := context.WithTimeout(ctx, visionClassifyTimeout)
	defer cancel()

	out, err := vision.GenerateWithImages(callCtx, visionSystemInstruction, visionUserText(caption, refs), images)
	if err != nil {
		return ArrivalClaim{}
	}

	return claimFromModelOutput(out, now)
}

func fetchImage(ctx context.Context, fetcher ImageFetcher, rawURL string) ([]byte, bool) {
	fetchCtx, cancel := context.WithTimeout(ctx, imageFetchTimeout)
	defer cancel()

	data, err := fetcher.Fetch(fetchCtx, rawURL, llm.MaxImageBytes)
	hasData := err == nil && len(data) > 0

	return data, hasData
}
