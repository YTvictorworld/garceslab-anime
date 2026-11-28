// Command subtranslate-eval is the evaluation tool of the self-hosted subtitle
// translation (Garceslab Anime, phase F1). It translates local subtitle files with a
// model served by llama.cpp's llama-server (or any OpenAI-compatible server), measures
// the result against reference translations and builds blind review sheets.
//
// See docs/subtitle-translation-eval.md. The eval data stays outside the repository.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"seanime/internal/subtranslate/ass"
	"seanime/internal/subtranslate/eval"
	"seanime/internal/subtranslate/lang"
	"seanime/internal/subtranslate/translate"
)

const usage = `subtranslate-eval: evaluate local subtitle translation models (phase F1).

Commands:
  init          create an eval directory with a manifest and two invented sample line sets
  check         check the llama-server endpoint and translate one invented line
  run           translate the manifest's items with the served model and save the results
  report        rebuild report.json and report.md from every saved run
  review        build a blind review sheet (sheet.csv, sheet.md) and its separate key
  review-score  tally a filled-in review sheet with its key
  languages     list the language registry

Run "subtranslate-eval <command> -h" for the flags of a command.
`

func defaultEvalDir() string {
	if d := os.Getenv("SUBTRANSLATE_EVAL_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "homelab-config", "seanime", "subtitle-translation-plan", "eval")
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	log := zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: "15:04:05"}).With().Timestamp().Logger()
	var err error
	switch os.Args[1] {
	case "init":
		err = cmdInit(os.Args[2:])
	case "check":
		err = cmdCheck(ctx, log, os.Args[2:])
	case "run":
		err = cmdRun(ctx, log, os.Args[2:])
	case "report":
		err = cmdReport(os.Args[2:])
	case "review":
		err = cmdReview(os.Args[2:])
	case "review-score":
		err = cmdReviewScore(os.Args[2:])
	case "languages":
		err = cmdLanguages(os.Args[2:])
	case "-h", "--help", "help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// refuseRepo stops when a data path is inside this repository: real subtitles and
// model outputs must never be committed.
func refuseRepo(paths ...string) error {
	for _, p := range paths {
		if root, ok := eval.RepoRoot(p); ok {
			return fmt.Errorf("%s is inside the repository (%s): keep eval data and results outside it (docs/subtitle-translation-eval.md)", p, root)
		}
		if root, ok := eval.InGitWorkTree(p); ok {
			fmt.Fprintf(os.Stderr, "warning: %s is inside the git work tree %s; make sure it is ignored there\n", p, root)
		}
	}
	return nil
}

func cmdInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	dir := fs.String("dir", defaultEvalDir(), "eval directory to create")
	_ = fs.Parse(args)
	if err := refuseRepo(*dir); err != nil {
		return err
	}
	written, err := eval.WriteSample(*dir)
	for _, w := range written {
		fmt.Println("created", w)
	}
	if err == nil && len(written) == 0 {
		fmt.Println("nothing to do: the files already exist in", *dir)
	}
	return err
}

type common struct {
	endpoint, apiKeyEnv, model, preset, presets, languages string
}

func (c *common) register(fs *flag.FlagSet) {
	fs.StringVar(&c.endpoint, "endpoint", "http://127.0.0.1:8080", "llama-server (or llama-swap / OpenAI-compatible) base URL")
	fs.StringVar(&c.apiKeyEnv, "api-key-env", "", "name of an environment variable holding an API key (optional)")
	fs.StringVar(&c.model, "model", "", "model name to request and to report (default: the first model the server lists)")
	fs.StringVar(&c.preset, "preset", "", "preset name (default: the presets file's default_preset)")
	fs.StringVar(&c.presets, "presets", "", "presets JSON file (default: the embedded presets.json)")
	fs.StringVar(&c.languages, "languages", "", "extra languages JSON file merged into the registry")
}

func (c *common) setup(ctx context.Context) (*translate.Client, *translate.Config, *translate.Preset, *lang.Registry, string, translate.ServerProps, error) {
	var props translate.ServerProps
	reg := lang.Default()
	if c.languages != "" {
		if err := reg.MergeFile(c.languages); err != nil {
			return nil, nil, nil, nil, "", props, err
		}
	}
	cfg := translate.DefaultConfig()
	if c.presets != "" {
		var err error
		if cfg, err = translate.LoadConfig(c.presets); err != nil {
			return nil, nil, nil, nil, "", props, err
		}
	}
	preset, err := cfg.Preset(c.preset)
	if err != nil {
		return nil, nil, nil, nil, "", props, err
	}
	client := translate.NewClient(c.endpoint)
	if c.apiKeyEnv != "" {
		client.APIKey = os.Getenv(c.apiKeyEnv)
	}
	model := c.model
	if model == "" && preset.Model != "" {
		model = preset.Model
	}
	if model == "" {
		models, err := client.Models(ctx)
		if err != nil {
			return nil, nil, nil, nil, "", props, fmt.Errorf("listing models at %s: %w", c.endpoint, err)
		}
		if len(models) == 0 {
			return nil, nil, nil, nil, "", props, errors.New("the server lists no model; pass -model")
		}
		model = filepath.Base(models[0])
	}
	props, _ = client.Props(ctx) // llama-server only
	return client, cfg, preset, reg, model, props, nil
}

func hintFor(cfg *translate.Config, model string, props translate.ServerProps) translate.ModelHint {
	return cfg.Hint(model + " " + filepath.Base(props.ModelPath))
}

func cmdCheck(ctx context.Context, log zerolog.Logger, args []string) error {
	fs := flag.NewFlagSet("check", flag.ExitOnError)
	var c common
	c.register(fs)
	source := fs.String("source", "en", "source language of the test line")
	target := fs.String("target", lang.DefaultTarget, "target language")
	text := fs.String("text", "", "test line in ASS markup (default: an invented line); printed, so never pass real subtitles")
	debugText := fs.Bool("debug-text", false, "log the prompt answer")
	_ = fs.Parse(args)
	if *debugText {
		log = log.Level(zerolog.DebugLevel)
	} else {
		log = log.Level(zerolog.InfoLevel)
	}
	client := translate.NewClient(c.endpoint)
	wait, err := client.WaitHealthy(ctx, 2*time.Minute)
	if err != nil {
		return err
	}
	fmt.Printf("health: ok (waited %.1fs)\n", wait.Seconds())
	client, cfg, preset, reg, model, props, err := c.setup(ctx)
	if err != nil {
		return err
	}
	hint := hintFor(cfg, model, props)
	fmt.Printf("model: %s  file: %s  ctx: %d  slots: %d  build: %s\n", model, filepath.Base(props.ModelPath), props.Ctx, props.TotalSlots, props.BuildInfo)
	fmt.Printf("mode: %s  no_think: %v  preset: %s\n", hint.Mode, hint.NoThink, preset.Name)
	if m, err := client.Metrics(ctx); err != nil {
		fmt.Println("metrics:", err)
	} else {
		fmt.Printf("metrics: ok (%d series)\n", len(m))
	}
	pair, err := reg.NewPair(*source, *target)
	if err != nil {
		return err
	}
	// An invented test line, safe to print.
	line0 := *text
	if line0 == "" {
		line0 = map[string]string{"fr": "Je te {\\i1}jure{\\i0} que c'est pas ma faute !"}[pair.Source.Base]
	}
	if line0 == "" {
		line0 = "I {\\i1}swear{\\i0} it wasn't my fault!"
	}
	tr := &translate.Translator{Client: client, Pair: pair, Preset: *preset, Model: model, Hint: hint, Log: log, DebugText: *debugText}
	line := translate.Line{ID: 0, End: 2 * time.Second, Masked: ass.Mask(line0)}
	res, st, err := tr.Translate(ctx, []translate.Line{line})
	if err != nil {
		return err
	}
	fmt.Printf("test line (%s): %s\n  -> %s\n  issues: %v  fallback: %v\n", pair, line0, res[0].Final, res[0].FirstIssues, res[0].Fallback)
	if st.PredictedMS > 0 {
		fmt.Printf("  %d prompt tokens, %d generated, %.1f tok/s generation, %.2fs wall\n", st.PromptTokens, st.CompletionTokens,
			float64(st.CompletionTokens)/st.PredictedMS*1000, st.Wall.Seconds())
	}
	return nil
}

func cmdRun(ctx context.Context, log zerolog.Logger, args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	var c common
	c.register(fs)
	evalDir := fs.String("eval-dir", defaultEvalDir(), "eval directory (holds manifest.json)")
	manifestPath := fs.String("manifest", "", "manifest file (default: <eval-dir>/manifest.json)")
	outDir := fs.String("out", "", "output directory (default: <eval-dir>/results)")
	only := fs.String("only", "", "comma-separated item ids or tags to run (default: all)")
	signs := fs.String("signs", "simple", "signs to translate: none or simple")
	limit := fs.Int("limit", 0, "max lines per item (0 = all)")
	chunk := fs.Int("chunk", 8, "first-chunk size for the warm start-up timing")
	backend := fs.String("backend", "unknown", "backend label recorded in the report (vulkan, cuda, cpu)")
	machine := fs.String("machine", "", "machine label recorded in the report")
	coldLoad := fs.Float64("cold-load", 0, "cold load time of the model in seconds, as printed by scripts/subtranslate-llama-server.sh")
	gpu := fs.String("gpu", "", "nvidia-smi GPU index for the peak VRAM (default: all)")
	debugText := fs.Bool("debug-text", os.Getenv("SEANIME_SUBTRANSLATE_DEBUG_TEXT") == "1", "log subtitle text, prompts and answers (development only)")
	_ = fs.Parse(args)

	if *manifestPath == "" {
		*manifestPath = filepath.Join(*evalDir, "manifest.json")
	}
	if *outDir == "" {
		*outDir = filepath.Join(filepath.Dir(*manifestPath), "results")
	}
	if *signs != "none" && *signs != "simple" {
		return fmt.Errorf("-signs must be none or simple")
	}
	if err := refuseRepo(*manifestPath, *outDir); err != nil {
		return err
	}
	m, err := eval.LoadManifest(*manifestPath)
	if err != nil {
		return err
	}
	client := translate.NewClient(c.endpoint)
	if _, err := client.WaitHealthy(ctx, 5*time.Minute); err != nil {
		return err
	}
	client, cfg, preset, reg, model, props, err := c.setup(ctx)
	if err != nil {
		return err
	}
	hint := hintFor(cfg, model, props)
	level := zerolog.InfoLevel
	if *debugText {
		level = zerolog.DebugLevel
		log.Warn().Msg("debug text is on: subtitle text will be logged")
	}
	log = log.Level(level)
	env := eval.Env{Endpoint: c.endpoint, Backend: *backend, Machine: *machine, ServerBuild: props.BuildInfo,
		ModelFile: filepath.Base(props.ModelPath), ServerCtx: props.Ctx, Slots: props.TotalSlots, ColdLoadS: *coldLoad}
	if env.ModelFile == "." {
		env.ModelFile = ""
	}
	log.Info().Str("model", model).Str("file", env.ModelFile).Str("preset", preset.Name).Str("mode", hint.Mode).
		Bool("no_think", hint.NoThink).Str("backend", env.Backend).Int("prompt_version", translate.PromptVersion).Msg("run")

	want := map[string]bool{}
	for _, s := range strings.Split(*only, ",") {
		if s = strings.TrimSpace(s); s != "" {
			want[s] = true
		}
	}
	vram := eval.StartVRAM(*gpu, 500*time.Millisecond)
	before, _ := client.Metrics(ctx)
	ran := 0
	for _, it := range m.Items {
		if len(want) > 0 && !selected(it, want) {
			continue
		}
		vram.Reset()
		o := eval.Options{Client: client, Registry: reg, Preset: preset, Model: model, Hint: hint, OutDir: *outDir,
			Signs: *signs, Limit: *limit, ChunkSize: *chunk, Log: log, DebugText: *debugText, Env: env}
		res, err := eval.RunItem(ctx, m, it, o)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			log.Error().Str("item", it.ID).Err(err).Msg("item failed")
			continue
		}
		res.Env.PeakVRAMMiB, res.Env.MaxTempC, res.Env.Throttled = vram.Read()
		if err := eval.SaveResult(*outDir, res); err != nil {
			return err
		}
		if res.Env.Throttled {
			log.Warn().Str("item", res.Item).Int("max_temp_c", res.Env.MaxTempC).Msg("the GPU slowed down for heat or power: this item's speed numbers are not representative")
		}
		ran++
		log.Info().Str("item", res.Item).Str("pair", res.Pair).Int("lines", res.Lines).
			Str("chrF++", fmt.Sprintf("%.1f", res.ChrFpp)).Int("fallback", res.Fallback).
			Int("tags_broken_first", res.TagsBrokenFirst).Int("tag_lines", res.TagLines).
			Int("source_lang", res.SourceLangLines).Int("retried_batches", res.RetriedBatches).
			Str("wall", fmt.Sprintf("%.1fs", res.WallS)).Str("first_chunk", fmt.Sprintf("%.2fs", res.FirstChunkS)).
			Str("gen_tok_s", fmt.Sprintf("%.1f", tokS(res.CompletionTokens, res.PredictedMS))).Msg("item done")
	}
	vram.Stop()
	peak, _, _ := vram.Read()
	if after, err := client.Metrics(ctx); err == nil && before != nil {
		log.Info().Float64("prompt_tokens", after["llamacpp:prompt_tokens_total"]-before["llamacpp:prompt_tokens_total"]).
			Float64("generated_tokens", after["llamacpp:tokens_predicted_total"]-before["llamacpp:tokens_predicted_total"]).
			Int("peak_vram_mib", peak).Msg("server metrics for this run")
	}
	if ran == 0 {
		return errors.New("no item ran")
	}
	rep, err := eval.WriteReport(*outDir)
	if err != nil {
		return err
	}
	fmt.Printf("report: %s (%d summaries)\n", filepath.Join(*outDir, "report.md"), len(rep.Summaries))
	return nil
}

func tokS(tokens int, ms float64) float64 {
	if ms <= 0 {
		return 0
	}
	return float64(tokens) / ms * 1000
}

func selected(it eval.Item, want map[string]bool) bool {
	if want[it.ID] {
		return true
	}
	for _, t := range it.Tags {
		if want[t] {
			return true
		}
	}
	return false
}

func cmdReport(args []string) error {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	out := fs.String("out", filepath.Join(defaultEvalDir(), "results"), "output directory of the runs")
	_ = fs.Parse(args)
	rep, err := eval.WriteReport(*out)
	if err != nil {
		return err
	}
	fmt.Printf("report: %s (%d summaries, %d item results)\n", filepath.Join(*out, "report.md"), len(rep.Summaries), len(rep.Items))
	return nil
}

func cmdReview(args []string) error {
	fs := flag.NewFlagSet("review", flag.ExitOnError)
	out := fs.String("out", filepath.Join(defaultEvalDir(), "results"), "output directory of the runs")
	runs := fs.String("runs", "", "comma-separated run directories under <out>/runs (\"<model>__<preset>\"; default: all)")
	lines := fs.Int("lines", 80, "lines to sample")
	seed := fs.Int64("seed", time.Now().UnixNano(), "random seed (recorded in the key)")
	reference := fs.String("reference", "visible", "reference: visible, blind (a hidden candidate) or none")
	pair := fs.String("pair", "", "only this language pair, e.g. en→es-419 or en-es-419")
	_ = fs.Parse(args)
	if *reference != "visible" && *reference != "blind" && *reference != "none" {
		return fmt.Errorf("-reference must be visible, blind or none")
	}
	if err := refuseRepo(*out); err != nil {
		return err
	}
	var list []string
	for _, r := range strings.Split(*runs, ",") {
		if r = strings.TrimSpace(r); r != "" {
			list = append(list, r)
		}
	}
	p := *pair
	if p != "" && !strings.Contains(p, "→") {
		if src, tgt, ok := strings.Cut(p, "-"); ok {
			p = src + "→" + tgt
		}
	}
	dir, err := eval.Review(eval.ReviewOptions{OutDir: *out, Runs: list, Lines: *lines, Seed: *seed, Reference: *reference, Pair: p})
	if err != nil {
		return err
	}
	fmt.Printf("review sheet: %s/sheet.csv (and sheet.md)\nkey (don't open before scoring): %s\n", dir, filepath.Join(*out, "review-key.json"))
	return nil
}

func cmdReviewScore(args []string) error {
	fs := flag.NewFlagSet("review-score", flag.ExitOnError)
	out := fs.String("out", filepath.Join(defaultEvalDir(), "results"), "output directory of the runs")
	sheet := fs.String("sheet", "", "filled-in sheet (default: <out>/review/sheet.csv)")
	key := fs.String("key", "", "key (default: <out>/review-key.json)")
	_ = fs.Parse(args)
	if *sheet == "" {
		*sheet = filepath.Join(*out, "review", "sheet.csv")
	}
	if *key == "" {
		*key = filepath.Join(*out, "review-key.json")
	}
	rows, err := eval.ScoreReview(*sheet, *key)
	if err != nil {
		return err
	}
	md := eval.ScoreMarkdown(rows)
	fmt.Print(md)
	return os.WriteFile(filepath.Join(*out, "review-score.md"), []byte(md), 0o644)
}

func cmdLanguages(args []string) error {
	fs := flag.NewFlagSet("languages", flag.ExitOnError)
	extra := fs.String("languages", "", "extra languages JSON file")
	_ = fs.Parse(args)
	reg := lang.Default()
	if *extra != "" {
		if err := reg.MergeFile(*extra); err != nil {
			return err
		}
	}
	for _, code := range reg.Codes() {
		l, _ := reg.Resolve(code)
		fmt.Printf("%-7s base=%-3s %s (%d target rules, %d source notes, %d function words)\n", l.Code, l.Base, l.Name,
			len(l.TargetRules), len(l.SourceNotes), len(l.FunctionWords))
	}
	return nil
}
