package capitime

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/HectorVeraO/chatisimo/internal/platform/llm"
)

const (
	readTimeout       = 9 * time.Second
	maxReadsPerMinute = 6
	readWindow        = time.Minute
	maxReadMinutes    = 720
)

// decision names the step that settled one evaluated message. It is the "decision" field of the
// decision log.
type decision string

const (
	decisionModelClaim       decision = "model_claim"
	decisionModelNoClaim     decision = "model_no_claim"
	decisionModelFailed      decision = "model_failed"
	decisionModelUnparseable decision = "model_unparseable"
	decisionRateLimited      decision = "rate_limited"
	decisionNoContent        decision = "no_content"
	decisionNoReader         decision = "no_reader"
)

// reachedModel reports whether the message was sent to the model, whatever came back.
func (d decision) reachedModel() bool {
	switch d {
	case decisionModelClaim, decisionModelNoClaim, decisionModelFailed, decisionModelUnparseable:
		return true
	default:
		return false
	}
}

// readSystemInstruction is the static system instruction of the arrival read. The Go side does all
// date arithmetic: the model only names the day, the clock time or the minutes the author gave.
const readSystemInstruction = "" +
	"Eres un clasificador. Recibes UN mensaje de Discord en espanol chileno escrito por una sola persona: " +
	"texto y, a veces, imagenes, stickers o un audio. Todo junto es un solo mensaje y respondes una sola vez.\n" +
	"A veces vienen antes lineas \"anterior:\": son mensajes que la misma persona envio en los ultimos minutos, " +
	"del mas antiguo al mas reciente, y son solo contexto. La linea \"mensaje:\", junto con las imagenes, " +
	"los stickers y el audio, es el ultimo mensaje y es el unico que clasificas; " +
	"si no hay linea \"mensaje:\", el ultimo mensaje son solo las imagenes, los stickers o el audio.\n" +
	"Decide si quien escribe anuncia, en primera persona o nombrandose con su propio apodo (Capi, Capis, Braulio, Braulitros), " +
	"que va a llegar, que se va a conectar o que va en camino, ahora o en el futuro. " +
	"El anuncio puede venir partido en varios mensajes: cuenta cuando el ultimo mensaje, " +
	"leido junto con las lineas \"anterior:\", lo hace o lo completa.\n" +
	"No es una llegada: hechos pasados; otras personas o cosas que llegan; negaciones; preguntas; " +
	"tramites o planes que no son sumarse; un mensaje que solo menciona la fecha o la hora actual; musica o ruido.\n" +
	"Si el ultimo mensaje no aporta nada a una llegada (un comentario aparte, una risa, otro tema), " +
	"no es una llegada aunque una linea \"anterior:\" haya anunciado una. " +
	"Una negacion partida sigue siendo negacion: si una linea \"anterior:\" niega y el ultimo mensaje " +
	"termina esa misma frase, no es una llegada.\n" +
	"Imagenes y stickers: lee el texto dibujado como si estuviera escrito en el mensaje. " +
	"Audio: cuenta solo lo que dice quien habla.\n" +
	"La linea \"ahora:\" trae el dia de la semana, la fecha y la hora actual; no es parte del mensaje.\n" +
	"Responde UNICAMENTE un objeto JSON en una linea con este formato: " +
	`{"arrival":false,"day":"","clock":"","minutes":0}` + "\n" +
	"- arrival: true solo si el ultimo mensaje, leido junto con las lineas \"anterior:\", " +
	"anuncia o completa el anuncio de la propia llegada o conexion de quien escribe; si no, false.\n" +
	"- day: el dia anunciado: \"hoy\", \"manana\", o un dia de la semana " +
	"(\"lunes\", \"martes\", \"miercoles\", \"jueves\", \"viernes\", \"sabado\", \"domingo\"); \"\" si no dice dia.\n" +
	"- clock: la hora anunciada en formato de 24 horas \"HH:MM\"; \"\" si no dice hora.\n" +
	"- minutes: los minutos desde ahora cuando anuncia un plazo; 0 si no dice plazo. Usa clock o minutes, nunca ambos.\n" +
	"El dia, la hora o el plazo pueden venir del ultimo mensaje o de las lineas \"anterior:\" del anuncio " +
	"que completa; nunca de la linea \"ahora:\".\n" +
	"Si anuncia que llega de inmediato o no dice cuando, deja day y clock vacios y minutes en 0: " +
	"no inventes una hora ni un plazo.\n" +
	"Horas vagas: \"en la noche\" es clock \"22:00\"; \"en la tarde\" es clock \"18:00\"; " +
	"\"en la manana\" es clock \"10:00\"; \"mas rato\" o \"en un rato\" es minutes 30.\n" +
	"Si arrival es false, deja day y clock vacios y minutes en 0."

// readAnswer is the JSON object the model returns. Arrival is a pointer so an answer without the
// field is told apart from "arrival": false.
type readAnswer struct {
	Arrival *bool  `json:"arrival"`
	Day     string `json:"day"`
	Clock   string `json:"clock"`
	Minutes int    `json:"minutes"`
}

var readClockRe = regexp.MustCompile(`^([01]?\d|2[0-3]):([0-5]\d)$`)

var spanishWeekdays = [...]string{"domingo", "lunes", "martes", "miercoles", "jueves", "viernes", "sabado"}

// readDay is a value of the "day" field of a readAnswer.
type readDay struct {
	isTomorrow bool
	isWeekday  bool
	weekday    time.Weekday
}

// parseReadDay maps the "day" field to a readDay. It accepts the accented spellings too and
// reports false for anything outside the contract.
func parseReadDay(raw string) (readDay, bool) {
	normalized := strings.NewReplacer("ñ", "n", "é", "e", "á", "a").Replace(strings.ToLower(strings.TrimSpace(raw)))

	switch normalized {
	case "", "hoy":
		return readDay{}, true
	case "manana":
		return readDay{isTomorrow: true}, true
	}

	for index, name := range spanishWeekdays {
		if normalized == name {
			return readDay{isWeekday: true, weekday: time.Weekday(index)}, true
		}
	}

	return readDay{}, false
}

// parseReadClock parses a whole-string 24-hour "HH:MM". hasClock is false for an empty value;
// isValid is false for anything else that is not a clock time.
func parseReadClock(raw string) (hour, minute int, hasClock, isValid bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, 0, false, true
	}

	sub := readClockRe.FindStringSubmatch(trimmed)
	if sub == nil {
		return 0, 0, false, false
	}

	hour, errHour := strconv.Atoi(sub[1])
	minute, errMinute := strconv.Atoi(sub[2])
	if errHour != nil || errMinute != nil {
		return 0, 0, false, false
	}

	return hour, minute, true, true
}

// claimFromReadAnswer turns the model's answer into an ArrivalClaim at reference instant now.
// It returns decisionModelClaim with the claim, decisionModelNoClaim when the model says there is
// no arrival, and decisionModelUnparseable when the answer is not the documented object or any
// field holds a value outside the contract; it never guesses.
//
// A weekday means its next occurrence (the same weekday is seven days ahead) at the clock time, or
// 20:00 without one. "manana" means tomorrow at the clock time, or 12:00 without one. A clock time
// alone, or with "hoy", is its next occurrence. Otherwise minutes counts from now, and an answer
// with no day, clock or minutes is an immediate arrival.
//
// When the model sets minutes together with a weekday, "manana" or a clock time, the day and the
// clock time win and minutes is ignored: the answer is still a claim, not an unparseable one.
func claimFromReadAnswer(out string, now time.Time) (ArrivalClaim, decision) {
	trimmed := strings.TrimSpace(out)
	trimmed = strings.TrimPrefix(trimmed, "```json")
	trimmed = strings.TrimPrefix(trimmed, "```")
	trimmed = strings.TrimSuffix(trimmed, "```")
	trimmed = strings.TrimSpace(trimmed)

	var answer readAnswer
	if err := json.Unmarshal([]byte(trimmed), &answer); err != nil || answer.Arrival == nil {
		return ArrivalClaim{}, decisionModelUnparseable
	}
	if !*answer.Arrival {
		return ArrivalClaim{}, decisionModelNoClaim
	}

	day, isKnownDay := parseReadDay(answer.Day)
	hour, minute, hasClock, isValidClock := parseReadClock(answer.Clock)
	isValidMinutes := answer.Minutes >= 0 && answer.Minutes <= maxReadMinutes
	if !isKnownDay || !isValidClock || !isValidMinutes {
		return ArrivalClaim{}, decisionModelUnparseable
	}

	ref := now.In(Santiago())
	claim := ArrivalClaim{Detected: true}

	switch {
	case day.isWeekday:
		daysAhead := (int(day.weekday) - int(ref.Weekday()) + 7) % 7
		if daysAhead == 0 {
			daysAhead = 7
		}
		if !hasClock {
			hour, minute = 20, 0
		}
		claim.ClaimedAt = dateAtClock(ref.AddDate(0, 0, daysAhead), hour, minute)
		claim.ClaimSummary = "el " + spanishWeekdays[day.weekday]
	case day.isTomorrow:
		if !hasClock {
			hour, minute = 12, 0
		}
		claim.ClaimedAt = dateAtClock(ref.AddDate(0, 0, 1), hour, minute)
		claim.ClaimSummary = "manana"
	case hasClock:
		claim.ClaimedAt = resolveNextClockInstant(ref, hour, minute)
		claim.ClaimSummary = fmt.Sprintf("%02d:%02d", hour, minute)
	case answer.Minutes > 0:
		claim.ClaimedAt = ref.Add(time.Duration(answer.Minutes) * time.Minute)
		claim.ClaimSummary = fmt.Sprintf("en %d min", answer.Minutes)
	default:
		claim.Immediate = true
		claim.ClaimedAt = ref
		claim.ClaimSummary = "ahora"
	}

	return claim, decisionModelClaim
}

// readUserText builds the user text of the read: the reference instant, then one line per earlier
// message of the same member, oldest first, then the message text when there is one, then one line
// per sticker name. The earlier texts must be sanitized, so each stays on its own line.
func readUserText(content messageContent, earlier []string, now time.Time) string {
	ref := now.In(Santiago())

	lines := make([]string, 0, len(earlier)+len(content.stickerNames)+2)
	lines = append(lines, "ahora: "+spanishWeekdays[ref.Weekday()]+" "+ref.Format("2006-01-02 15:04"))
	for _, text := range earlier {
		lines = append(lines, "anterior: "+text)
	}
	if content.text != "" {
		lines = append(lines, "mensaje: "+content.text)
	}
	for _, name := range content.stickerNames {
		lines = append(lines, "sticker: "+name)
	}

	return strings.Join(lines, "\n")
}

// readArrival asks the model, in one request, whether content announces an arrival, alone or as the
// end of an announcement started in earlier, the sanitized texts the same member sent just before,
// oldest first. content must not be empty. A failed request yields decisionModelFailed.
func readArrival(
	ctx context.Context,
	reader ArrivalReader,
	content messageContent,
	earlier []string,
	now time.Time,
) (ArrivalClaim, decision) {
	callCtx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()

	out, err := reader.GenerateWithMedia(callCtx, llm.MediaRequest{
		Prompt: readSystemInstruction,
		Text:   readUserText(content, earlier, now),
		Images: content.images,
		Audio:  content.audio,
		JSON:   true,
	})
	if err != nil {
		return ArrivalClaim{}, decisionModelFailed
	}

	return claimFromReadAnswer(out, now)
}

// readLimiter caps the model reads of one guild member to maxReadsPerMinute in any one-minute
// window. It lives in memory: a restart forgets the window.
type readLimiter struct {
	mu    sync.Mutex
	reads map[string][]time.Time
}

func newReadLimiter() *readLimiter {
	return &readLimiter{reads: make(map[string][]time.Time)}
}

// recentLocked drops the reads of key that left the window ending at now and returns the rest. It
// must run with l.mu held.
func (l *readLimiter) recentLocked(key string, now time.Time) []time.Time {
	var recent []time.Time
	for _, at := range l.reads[key] {
		age := now.Sub(at)
		if age >= 0 && age < readWindow {
			recent = append(recent, at)
		}
	}

	if len(recent) == 0 {
		delete(l.reads, key)
	} else {
		l.reads[key] = recent
	}

	return recent
}

// hasRoom reports whether key may read at now, without counting a read.
func (l *readLimiter) hasRoom(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	return len(l.recentLocked(key, now)) < maxReadsPerMinute
}

// take counts one read of key at now and reports true, or reports false when key is at the cap.
func (l *readLimiter) take(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	recent := l.recentLocked(key, now)
	if len(recent) >= maxReadsPerMinute {
		return false
	}

	l.reads[key] = append(recent, now)

	return true
}
