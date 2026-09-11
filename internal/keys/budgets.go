package keys

// Budget is the in-memory resource view of the existing on-disk limit fields.
// Request-rate, concurrency, output/context ceilings and model policy remain in Limits.
type Budget struct {
	Unit   string
	Window string
	Amount int
}

type Budgets map[string][]Budget

// Budgets derives a fresh view so hot edits and legacy audio defaults apply without
// rewriting keys.json or maintaining a second mutable copy of the limits.
func (l Limits) Budgets() Budgets {
	l = AudioDefaults(l)
	return Budgets{
		"search": {{"requests", "day", l.SearchPerDay}},
		"images": {{"images", "day", l.DailyImages}},
		"tokens": {{"tokens", "minute", l.TPM}, {"tokens", "day", l.DailyTokens}},
		"audio":  {{"seconds", "day", l.DailyAudioSeconds}},
		"speech": {{"characters", "day", l.DailySpeechChars}},
	}
}

func (b Budgets) Amount(class, window string) int {
	for _, limit := range b[class] {
		if limit.Window == window {
			return limit.Amount
		}
	}
	return 0
}
