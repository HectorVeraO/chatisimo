package capitime

import (
	"context"
	"math"
	"time"

	"github.com/HectorVeraO/chatisimo/internal/platform/llm"
)

const (
	audioFetchTimeout    = 5 * time.Second
	audioClassifyTimeout = 9 * time.Second
)

const audioInstructionExtra = "" +
	"La entrada es un mensaje de voz o un archivo de audio de una persona que habla espanol chileno, " +
	"posiblemente con un texto de acompanamiento. " +
	"Escucha si quien habla anuncia su propia llegada o conexion " +
	"(llego, voy, entrando, ya voy, me conecto, en 10 minutos, a las 22:00, etc.) y tratalo como si estuviera " +
	"escrito en el mensaje. " +
	"Responde arrival:false para musica, sonidos de juegos, ruido de fondo, silencio, relatos en pasado " +
	"o si habla de otras personas. "

const audioSystemInstruction = stage2Rules + audioInstructionExtra + stage2JSONContract

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

// classifyAudioWithModel downloads the first reference of refs and asks the model whether the audio,
// together with the caption, announces an arrival. At most one audio per message is read: the rest
// of refs is ignored. Any fetch or model failure yields no claim. refs must come from usableAudioRefs.
func classifyAudioWithModel(
	ctx context.Context,
	audioModel AudioGenerator,
	fetcher ImageFetcher,
	refs []AudioRef,
	caption string,
	now time.Time,
) ArrivalClaim {
	if audioModel == nil || fetcher == nil || ctx == nil || len(refs) == 0 {
		return ArrivalClaim{}
	}

	ref := refs[0]

	data, ok := fetchAudio(ctx, fetcher, ref.URL)
	if !ok {
		return ArrivalClaim{}
	}

	callCtx, cancel := context.WithTimeout(ctx, audioClassifyTimeout)
	defer cancel()

	audio := llm.Audio{MIMEType: ref.MIMEType, Data: data}

	out, err := audioModel.GenerateWithAudio(callCtx, audioSystemInstruction, sanitizeInputText(caption), audio)
	if err != nil {
		return ArrivalClaim{}
	}

	return claimFromModelOutput(out, now)
}

func fetchAudio(ctx context.Context, fetcher ImageFetcher, rawURL string) ([]byte, bool) {
	fetchCtx, cancel := context.WithTimeout(ctx, audioFetchTimeout)
	defer cancel()

	data, err := fetcher.Fetch(fetchCtx, rawURL, llm.MaxAudioBytes)
	hasData := err == nil && len(data) > 0

	return data, hasData
}
