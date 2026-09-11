package discussion

import (
	"context"
	"errors"
	"math/rand"
	"strconv"
	"strings"
	"testing"

	"github.com/maxdollinger/meeth-councile/internal/llm"
	"github.com/maxdollinger/meeth-councile/internal/memory"
)

type hearCall struct {
	speaker string
	content string
}

const testTopic = "what is the good?"

type fakeSpeaker struct {
	name         string
	lastModel    string
	models       []string
	spokenModels []string
	hears        []hearCall
	speakN       int
	respond      func(n int) (string, error)
	output       llm.Input
	usage        llm.Usage
	speakErr     error
	hearErr      error
}

func (f *fakeSpeaker) Name() string { return f.name }

func (f *fakeSpeaker) UseModel(model string) error {
	if strings.TrimSpace(model) == "" {
		return errors.New("blank model")
	}
	f.lastModel = model
	f.models = append(f.models, model)
	return nil
}

func (f *fakeSpeaker) Speak(context.Context) (string, string, llm.Usage, llm.Input, error) {
	f.speakN++
	if f.speakErr != nil {
		return "", "", llm.Usage{}, nil, f.speakErr
	}
	f.spokenModels = append(f.spokenModels, f.lastModel)
	content := "turn " + strconv.Itoa(f.speakN)
	if f.respond != nil {
		c, err := f.respond(f.speakN)
		if err != nil {
			return "", "", llm.Usage{}, nil, err
		}
		content = c
	}
	output := f.output
	if output == nil {
		output = llm.Input{llm.Assistant(content)}
	}
	return f.name, content, f.usage, output, nil
}

func (f *fakeSpeaker) Hear(_ context.Context, name, content string) (memory.Understanding, llm.Usage, error) {
	if f.hearErr != nil {
		return memory.Understanding{}, llm.Usage{}, f.hearErr
	}
	f.hears = append(f.hears, hearCall{speaker: name, content: content})
	return memory.Understanding{Speaker: name, Content: content}, f.usage, nil
}

func (f *fakeSpeaker) HearDirect(_ context.Context, name, content string) (memory.Understanding, error) {
	if f.hearErr != nil {
		return memory.Understanding{}, f.hearErr
	}
	f.hears = append(f.hears, hearCall{speaker: name, content: content})
	return memory.Understanding{Speaker: name, Content: content}, nil
}

type fakeStore struct {
	turns   []Turn
	entries []SpeakEntry
	calls   []Call
	err     error
}

func (f *fakeStore) Append(_ context.Context, t Turn) error {
	if f.err != nil {
		return f.err
	}
	f.turns = append(f.turns, t)
	return nil
}

func (f *fakeStore) AppendSpeakEntry(_ context.Context, e SpeakEntry) error {
	if f.err != nil {
		return f.err
	}
	f.entries = append(f.entries, e)
	return nil
}

func (f *fakeStore) AppendCall(_ context.Context, c Call) error {
	if f.err != nil {
		return f.err
	}
	f.calls = append(f.calls, c)
	return nil
}

func speaker(name string) *fakeSpeaker { return &fakeSpeaker{name: name} }

func newDiscussion(t *testing.T, speakers []Speaker, models []string, opts ...Option) *Discussion {
	t.Helper()
	if models == nil {
		models = []string{"m1", "m2", "m3", "m4"}
	}
	d, err := New(testTopic, speakers, models, &fakeStore{}, opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d
}

func alwaysPass(n int) (string, error)    { return "PASS", nil }
func alwaysContent(n int) (string, error) { return "point " + strconv.Itoa(n), nil }

func TestNewValidation(t *testing.T) {
	valid := []Speaker{speaker("a"), speaker("b")}
	models := []string{"m1"}
	store := &fakeStore{}
	nilSpeaker := []Speaker{speaker("a"), nil}
	dupes := []Speaker{speaker("a"), speaker("a")}
	reserved := []Speaker{speaker("moderator")}
	blankName := []Speaker{&fakeSpeaker{name: ""}}

	cases := []struct {
		name      string
		topic     string
		speakers  []Speaker
		models    []string
		store     Store
		wantError bool
	}{
		{"valid", "topic", valid, models, store, false},
		{"blank topic", "  ", valid, models, store, true},
		{"no speakers", "topic", nil, models, store, true},
		{"nil speaker", "topic", nilSpeaker, models, store, true},
		{"blank name", "topic", blankName, models, store, true},
		{"reserved name", "topic", reserved, models, store, true},
		{"duplicate", "topic", dupes, models, store, true},
		{"no models", "topic", valid, nil, store, true},
		{"blank model", "topic", valid, []string{"  "}, store, true},
		{"nil store", "topic", valid, models, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.topic, tc.speakers, tc.models, tc.store)
			if tc.wantError && err == nil {
				t.Fatal("want error, got nil")
			}
			if !tc.wantError && err != nil {
				t.Fatalf("want nil error, got %v", err)
			}
		})
	}
}

func TestRunAllPassEndsAfterOneRound(t *testing.T) {
	a, b, c := speaker("a"), speaker("b"), speaker("c")
	for _, s := range []*fakeSpeaker{a, b, c} {
		s.respond = alwaysPass
	}
	d := newDiscussion(t, []Speaker{a, b, c}, nil, WithRand(rand.New(rand.NewSource(1))))

	res, err := d.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Ended != EndedAllPassed {
		t.Errorf("Ended = %q, want %q", res.Ended, EndedAllPassed)
	}
	if res.Rounds != 1 {
		t.Errorf("Rounds = %d, want 1", res.Rounds)
	}
	if len(res.Turns) != 4 {
		t.Fatalf("turns = %d, want 1 opening + 3 passes", len(res.Turns))
	}
	if res.Turns[0].Speaker != moderator || res.Turns[0].Content != "what is the good?" {
		t.Errorf("opening = %+v, want moderator topic", res.Turns[0])
	}
	for _, turn := range res.Turns[1:] {
		if !turn.Passed {
			t.Errorf("turn = %+v, want Passed", turn)
		}
	}
	for _, s := range []*fakeSpeaker{a, b, c} {
		if len(s.hears) != 1 || s.hears[0].speaker != moderator {
			t.Errorf("%s hears = %+v, want only the moderator opening", s.name, s.hears)
		}
	}
}

func TestRunHearsOnlyNonPass(t *testing.T) {
	a, b, c := speaker("a"), speaker("b"), speaker("c")
	a.respond = func(n int) (string, error) {
		if n == 1 {
			return "alpha", nil
		}
		return "PASS", nil
	}
	b.respond = alwaysPass
	c.respond = func(n int) (string, error) {
		if n == 1 {
			return "gamma", nil
		}
		return "PASS", nil
	}
	d := newDiscussion(t, []Speaker{a, b, c}, nil, WithRand(rand.New(rand.NewSource(7))))

	if _, err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	want := map[string]map[string]bool{
		"a": {testTopic: true, "gamma": true},
		"b": {testTopic: true, "alpha": true, "gamma": true},
		"c": {testTopic: true, "alpha": true},
	}
	for _, s := range []*fakeSpeaker{a, b, c} {
		got := make(map[string]bool)
		for _, h := range s.hears {
			if h.content == "PASS" {
				t.Errorf("%s heard a PASS", s.name)
			}
			got[h.content] = true
		}
		if len(got) != len(want[s.name]) {
			t.Errorf("%s heard %v, want %v", s.name, got, want[s.name])
		}
		for content := range want[s.name] {
			if !got[content] {
				t.Errorf("%s did not hear %q", s.name, content)
			}
		}
	}
}

func TestRunOrderConstraintAndPermutation(t *testing.T) {
	names := []string{"a", "b", "c", "d"}
	for seed := int64(0); seed < 25; seed++ {
		speakers := make([]*fakeSpeaker, len(names))
		roster := make([]Speaker, len(names))
		for i, name := range names {
			speakers[i] = speaker(name)
			speakers[i].respond = alwaysContent
			roster[i] = speakers[i]
		}
		d := newDiscussion(t, roster, nil, WithRand(rand.New(rand.NewSource(seed))), WithMaxRounds(4))
		res, err := d.Run(context.Background())
		if err != nil {
			t.Fatalf("seed %d: Run: %v", seed, err)
		}

		rounds := groupRounds(res.Turns)
		if len(rounds) != 4 {
			t.Fatalf("seed %d: rounds = %d, want 4", seed, len(rounds))
		}
		prevCloser := ""
		for n := 1; n <= 4; n++ {
			turns := rounds[n]
			if len(turns) != len(names) {
				t.Fatalf("seed %d round %d: turns = %d, want %d", seed, n, len(turns), len(names))
			}
			seen := make(map[string]bool)
			for _, turn := range turns {
				if seen[turn.Speaker] {
					t.Fatalf("seed %d round %d: %s spoke twice", seed, n, turn.Speaker)
				}
				seen[turn.Speaker] = true
			}
			if n > 1 && turns[0].Speaker == prevCloser {
				t.Fatalf("seed %d round %d: %s opened after closing the previous round", seed, n, prevCloser)
			}
			prevCloser = turns[len(turns)-1].Speaker
		}
	}
}

func TestRunAssignsModelPerSpeakerPerRound(t *testing.T) {
	pool := []string{"m1", "m2", "m3", "m4"}
	speakers := []*fakeSpeaker{speaker("a"), speaker("b"), speaker("c")}
	roster := make([]Speaker, len(speakers))
	for i, s := range speakers {
		s.respond = alwaysContent
		roster[i] = s
	}
	d := newDiscussion(t, roster, pool, WithRand(rand.New(rand.NewSource(3))), WithMaxRounds(3))
	res, err := d.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	inPool := func(m string) bool {
		for _, p := range pool {
			if p == m {
				return true
			}
		}
		return false
	}
	for _, s := range speakers {
		if len(s.models) != 1+res.Rounds {
			t.Errorf("%s UseModel calls = %d, want 1 opening + %d rounds", s.name, len(s.models), res.Rounds)
		}
		for _, m := range s.models {
			if !inPool(m) {
				t.Errorf("%s model %q not in pool", s.name, m)
			}
		}
	}

	byName := make(map[string][]string)
	for _, turn := range res.Turns {
		if turn.Round == 0 {
			continue
		}
		if !inPool(turn.Model) {
			t.Errorf("turn %+v: model not in pool", turn)
		}
		byName[turn.Speaker] = append(byName[turn.Speaker], turn.Model)
	}
	for _, s := range speakers {
		got, want := byName[s.name], s.spokenModels
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s turn models = %v, want the models passed to Speak %v", s.name, got, want)
		}
	}
}

func TestRunMaxRoundsCap(t *testing.T) {
	a, b := speaker("a"), speaker("b")
	a.respond = alwaysContent
	b.respond = alwaysContent
	d := newDiscussion(t, []Speaker{a, b}, nil, WithRand(rand.New(rand.NewSource(1))), WithMaxRounds(2))

	res, err := d.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Ended != EndedMaxRounds {
		t.Errorf("Ended = %q, want %q", res.Ended, EndedMaxRounds)
	}
	if res.Rounds != 2 {
		t.Errorf("Rounds = %d, want 2", res.Rounds)
	}
	if len(res.Turns) != 1+2*2 {
		t.Errorf("turns = %d, want 1 + 2 rounds", len(res.Turns))
	}
}

func TestRunStoreErrorAborts(t *testing.T) {
	a := speaker("a")
	a.respond = alwaysContent
	store := &fakeStore{err: errors.New("boom")}
	d, err := New("topic", []Speaker{a}, []string{"m1"}, store)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	res, err := d.Run(context.Background())
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if len(res.Turns) != 0 {
		t.Errorf("turns = %d, want 0", len(res.Turns))
	}
}

func TestRunSpeakErrorAborts(t *testing.T) {
	a, b := speaker("a"), speaker("b")
	a.respond = alwaysContent
	b.speakErr = errors.New("model down")
	d := newDiscussion(t, []Speaker{a, b}, nil, WithRand(rand.New(rand.NewSource(1))))

	res, err := d.Run(context.Background())
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if len(res.Turns) == 0 || res.Turns[0].Speaker != moderator {
		t.Errorf("turns = %+v, want the opening recorded", res.Turns)
	}
}

func TestRunHearErrorAborts(t *testing.T) {
	a, b := speaker("a"), speaker("b")
	a.respond = alwaysContent
	b.respond = alwaysPass
	b.hearErr = errors.New("memory down")
	d := newDiscussion(t, []Speaker{a, b}, nil, WithRand(rand.New(rand.NewSource(1))))

	if _, err := d.Run(context.Background()); err == nil {
		t.Fatal("want error, got nil")
	}
}

func TestRunRecordsOnlyAnswer(t *testing.T) {
	a := speaker("a")
	a.respond = func(int) (string, error) { return "the answer", nil }
	a.output = llm.Input{
		llm.Reasoning{Summary: []llm.ReasoningSummary{{Text: "think"}}},
		llm.FunctionCall{CallID: "c1", Name: "research", Arguments: `{"query":"x"}`},
		llm.FunctionCallOutput{CallID: "c1", Output: "result"},
		llm.Assistant("the answer"),
	}
	store := &fakeStore{}
	d, err := New(testTopic, []Speaker{a}, []string{"m1"}, store, WithMaxRounds(1))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	res, err := d.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	wantKinds := []string{"opening", "message"}
	if len(res.Entries) != len(wantKinds) {
		t.Fatalf("entries = %d, want %d", len(res.Entries), len(wantKinds))
	}
	for i, kind := range wantKinds {
		if res.Entries[i].Kind != kind {
			t.Errorf("entry[%d].Kind = %q, want %q", i, res.Entries[i].Kind, kind)
		}
	}
	if got := res.Entries[1].Content; got != "the answer" {
		t.Errorf("answer = %q, want the final message", got)
	}
	if len(store.entries) != len(wantKinds) {
		t.Errorf("stored entries = %d, want %d", len(store.entries), len(wantKinds))
	}
}

func TestRunMarksPassEntry(t *testing.T) {
	a := speaker("a")
	a.respond = alwaysPass
	store := &fakeStore{}
	d, err := New(testTopic, []Speaker{a}, []string{"m1"}, store)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	res, err := d.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Entries) != 2 {
		t.Fatalf("entries = %d, want opening + pass", len(res.Entries))
	}
	if res.Entries[1].Kind != kindPass {
		t.Errorf("pass entry kind = %q, want %q", res.Entries[1].Kind, kindPass)
	}
}

func TestRunRecordsModelCallsWithUsage(t *testing.T) {
	a, b := speaker("a"), speaker("b")
	a.usage = llm.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15, Cost: 0.01}
	b.usage = llm.Usage{InputTokens: 20, OutputTokens: 7, TotalTokens: 27, Cost: 0.02}
	a.respond = func(n int) (string, error) {
		if n == 1 {
			return "alpha", nil
		}
		return "PASS", nil
	}
	b.respond = alwaysPass
	st := &fakeStore{}
	d, err := New(testTopic, []Speaker{a, b}, []string{"m1"}, st,
		WithRand(rand.New(rand.NewSource(1))), WithMaxRounds(1))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	res, err := d.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	speaks, understands := 0, 0
	for _, c := range res.Calls {
		if c.Round != 1 {
			t.Errorf("call round = %d, want 1", c.Round)
		}
		if c.Model != "m1" {
			t.Errorf("call model = %q, want m1", c.Model)
		}
		switch c.Purpose {
		case "speak":
			speaks++
		case "understand":
			understands++
		default:
			t.Errorf("unexpected purpose %q", c.Purpose)
		}
	}
	if speaks != 2 {
		t.Errorf("speak calls = %d, want 2", speaks)
	}
	if understands != 1 {
		t.Errorf("understand calls = %d, want 1", understands)
	}
	if len(res.Calls) != len(st.calls) {
		t.Errorf("stored calls = %d, want %d", len(st.calls), len(res.Calls))
	}
}

func groupRounds(turns []Turn) map[int][]Turn {
	out := make(map[int][]Turn)
	for _, turn := range turns {
		if turn.Round == 0 {
			continue
		}
		out[turn.Round] = append(out[turn.Round], turn)
	}
	return out
}
