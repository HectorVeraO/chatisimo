// Package templates provides deterministic Chilean Spanish and English fallback banter lines for
// Capi Time arrival predictions.
package templates

import "fmt"

// Params holds the formatted fields used to render a deterministic Capi Time banter clause.
type Params struct {
	ClaimSummary string
	ClaimedClock string
	CapiClock    string
	DelayMinutes int
	Immediate    bool
	EasterEgg    bool
	Variant      int
}

var standardESCL = []func(Params) string{
	func(p Params) string {
		return fmt.Sprintf(
			"Dijo %q (%s), pero en Capi Time son +%d min mi king, as\u00ed que reci\u00e9n cae tipo %s; cuida a la bendi mientras tanto.",
			p.ClaimSummary, p.ClaimedClock, p.DelayMinutes, p.CapiClock,
		)
	},
	func(p Params) string {
		return fmt.Sprintf(
			"Traducci\u00f3n oficial de %q (%s): sumale %d min de Capi Time mi rey, llega a las %s y qued\u00f3 pana el atraso.",
			p.ClaimSummary, p.ClaimedClock, p.DelayMinutes, p.CapiClock,
		)
	},
	func(p Params) string {
		return fmt.Sprintf(
			"Alerta de humo con ese %q (%s), mi king: con +%d min de Capi Time asoma reci\u00e9n a las %s despuecito de dormir a la bendi.",
			p.ClaimSummary, p.ClaimedClock, p.DelayMinutes, p.CapiClock,
		)
	},
	func(p Params) string {
		return fmt.Sprintf(
			"El cl\u00e1sico %q (%s) del Capi: aplicale +%d min de peaje mi rey, cae a las %s y qued\u00f3 pana la espera.",
			p.ClaimSummary, p.ClaimedClock, p.DelayMinutes, p.CapiClock,
		)
	},
}

var easterEggESCL = []func(Params) string{
	func(p Params) string {
		return fmt.Sprintf(
			"Milagro hist\u00f3rico mi king: dijo %q y por primera vez el Capi llega puntual a las %s, qued\u00f3 pana y hasta la bendi aplaude.",
			p.ClaimSummary, p.ClaimedClock,
		)
	},
	func(p Params) string {
		return fmt.Sprintf(
			"Pidan un deseo mi rey: %q sin atraso, hoy se alinearon los astros y cae clavado a las %s, qued\u00f3 pana.",
			p.ClaimSummary, p.ClaimedClock,
		)
	},
}

var standardENUS = []func(Params) string{
	func(p Params) string {
		return fmt.Sprintf(
			"Claimed %q (%s), which translates to +%d min in Capi Time mi king: expect arrival around %s while la bendi waits.",
			p.ClaimSummary, p.ClaimedClock, p.DelayMinutes, p.CapiClock,
		)
	},
	func(p Params) string {
		return fmt.Sprintf(
			"Official Capi Time conversion for %q (%s): add %d min mi rey, landing at %s and quedo pana.",
			p.ClaimSummary, p.ClaimedClock, p.DelayMinutes, p.CapiClock,
		)
	},
}

var easterEggENUS = []func(Params) string{
	func(p Params) string {
		return fmt.Sprintf(
			"Miracle alert mi king: claimed %q and Capi is actually arriving on time at %s, quedo pana!",
			p.ClaimSummary, p.ClaimedClock,
		)
	},
}

// RenderESCL returns a deterministic single-line Chilean Spanish banter clause for p.
func RenderESCL(p Params) string {
	claim := normalizeClaim(p, "ahora")
	p.ClaimSummary = claim

	if p.EasterEgg {
		idx := nonNegativeIndex(p.Variant, len(easterEggESCL))
		return easterEggESCL[idx](p)
	}

	idx := nonNegativeIndex(p.Variant, len(standardESCL))
	return standardESCL[idx](p)
}

// RenderENUS returns a deterministic single-line English banter clause with Chilean group-chat
// flavor for p.
func RenderENUS(p Params) string {
	claim := normalizeClaim(p, "right now")
	p.ClaimSummary = claim

	if p.EasterEgg {
		idx := nonNegativeIndex(p.Variant, len(easterEggENUS))
		return easterEggENUS[idx](p)
	}

	idx := nonNegativeIndex(p.Variant, len(standardENUS))
	return standardENUS[idx](p)
}

func normalizeClaim(p Params, fallback string) string {
	if p.ClaimSummary != "" {
		return p.ClaimSummary
	}
	if p.ClaimedClock != "" {
		return p.ClaimedClock
	}

	return fallback
}

func nonNegativeIndex(variant, length int) int {
	if length <= 0 {
		return 0
	}
	rem := variant % length
	if rem < 0 {
		rem += length
	}

	return rem
}
