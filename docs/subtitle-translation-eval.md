# Subtitle translation: evaluation tool (phase F1)

Garceslab Anime will translate a release's subtitles with a **local** LLM when no release has
the profile's language (default: Latin American Spanish, `es-419`). Before building the
service, phase F1 compares models and fixes the prompt with numbers. This page explains the
tool that does it, `cmd/subtranslate-eval`: how to prepare the eval set, run it, and read the
report.

Nothing here runs inside the server yet. The tool talks to **llama.cpp's `llama-server`**
(directly, or behind `llama-swap` as in production) through its OpenAI-compatible API, so
everything stays on your machine.

- [What it measures](#what-it-measures)
- [Setting it up](#setting-it-up)
- [The eval set](#the-eval-set) (layout, manifest, line sets)
- [Running it](#running-it)
- [Reading the report](#reading-the-report)
- [Blind review](#blind-review)
- [Languages](#languages) (adding a source or target language)
- [Presets and models](#presets-and-models)
- [Privacy](#privacy)
- [Code layout and limits of F1](#code-layout-and-limits-of-f1)

## What it measures

For every item (an episode, or a set of hand-written lines), language pair and model:

| Kind | Metric |
|---|---|
| **Blocking checks** (a line failing one is retried once, then left in the source language) | same ids and count; the same tags `⟦n⟧` in the same order (ASS `{\i1}…{\i0}`, `{\c…}`, SRT `<i>`); at most one `⟦N⟧` line break (or as many as the source had); no empty line; output in the target language; length 0.5–2.2× the source; numbers kept; no translator notes; no copied context line; no loop (same text for several lines) |
| Also checked on the output file | the same events with the same timings; karaoke `\k`, drawings `\p1`, effects and songs untouched |
| **Indicative quality** | chrF++ and chrF against the reference (the same numbers sacreBLEU 2.x gives with its defaults), per item, per pair and per line-set category |
| **Rates** | % of lines failing a check on the first try, % broken tags on the first try (of lines with tags) and in the final file, % lines left in the source language, % fallback, % over 20 characters/s, % of batches retried |
| **Speed** | prefill and generation tokens/s (llama-server's `timings`), the first chunk of 8 lines with the model warm, total wall time and × real time, peak VRAM, GPU temperature and whether it throttled, the cold load time |

COMET is not computed (it needs a 2.3 GB model). If you want it, run it yourself on the saved
translations (`results/runs/*/*.translated.*`).

## Setting it up

**llama-server.** Any recent llama.cpp build (CUDA, Vulkan or CPU). The flags come from the
plan (`-fa on`, KV cache `q8_0`, two slots, `--jinja`, `--temp 0.2 --seed 42`, `--metrics`,
`--no-webui`); `scripts/subtranslate-llama-server.sh` starts one model with them, waits until it
is loaded and prints the cold load time:

```sh
# Vulkan build with the NVIDIA GPU as the second Vulkan device (list them with
# llama-server --list-devices)
LLAMA_SERVER=~/llama.cpp/build/bin/llama-server LLAMA_DEVICE=Vulkan1 \
  scripts/subtranslate-llama-server.sh ~/models/Qwen_Qwen3-4B-Instruct-2507-Q4_K_M.gguf
# -> model loaded in 3.9s: http://127.0.0.1:8080 (pass -cold-load 3.9 to subtranslate-eval run)
```

The script listens on `127.0.0.1:8080` and adapts a few flags per model: TranslateGemma gets
2K of context per slot (its trained context) and hybrid Qwen3 / Qwen3.5 models get
`--reasoning off`. Other settings are environment variables (`LLAMA_CTX`, `LLAMA_SLOTS`,
`LLAMA_NGL`, `LLAMA_PORT`…, see the script's header); extra arguments go to llama-server. The
default context is 8192 (4K per slot): with a 4 GB GPU and the Vulkan backend the plan's 16384
runs out of VRAM. The first request after loading is much slower than the rest (Vulkan
compiles its shaders): the tool sends a warm-up request before timing the first chunk.

**The tool.** Build it with Go, or with the Go image if Go isn't installed:

```sh
go build -o ~/bin/subtranslate-eval ./cmd/subtranslate-eval

docker run --rm -u "$(id -u):$(id -g)" -e HOME=/tmp -e GOCACHE=/tmp/go/cache -e GOMODCACHE=/tmp/go/mod \
  -v "$PWD:/src" -v "$HOME/.cache/garceslab-go:/tmp/go" -w /src golang:1.27.1 \
  go build -o /tmp/go/subtranslate-eval ./cmd/subtranslate-eval    # -> ~/.cache/garceslab-go/subtranslate-eval
```

Check the server and one invented line:

```sh
subtranslate-eval check            # en -> es-419
subtranslate-eval check -source fr # fr -> es-419
```

## The eval set

The eval set holds real subtitles and official translations, which are copyrighted works:
it lives **outside the repository**. The default directory is
`~/homelab-config/seanime/subtitle-translation-plan/eval/` (change it with `-eval-dir` or
`SUBTRANSLATE_EVAL_DIR`). The tool refuses to read or write eval data inside this repository,
and `TestNoEvalDataInRepo` fails if subtitle files, manifests or results get into it.

`subtranslate-eval init` creates the directory with a manifest and two tiny invented line
sets, to try the tool before preparing the real set.

### Layout

```
eval/
  manifest.json                         the list of items (below)
  lines/
    regression.en.jsonl                 ~150 lines written by us, English
    regression.fr.jsonl                 ~60 lines written by us, French
  episodes/
    <show>/<show>-e01.en.ass            source track (English)
    <show>/<show>-e01.es-419.ass        reference: the official es-419 track of the same file
    <other-show>/<other-show>-e05.fr.ass
    <other-show>/<other-show>-e05.es-419.ass
  results/                              written by the tool (one folder per model and preset)
```

Name files `<show>-e<NN>.<language>.<ext>`, with the language as a code the registry knows
(`en`, `fr`, `es-419`, `es-ES`…). ASS/SSA, SRT and line sets (`.jsonl`) are supported.

**What to collect (plan §3.4):**

1. **Line sets written by us** in a realistic style, with your reference translation:
   ~150 English and ~60 French lines across the categories below.
2. **3–4 real episodes with an official `es-419` track** and an English track from the same
   file (multi-sub Crunchyroll releases), plus **2–3 with French and Spanish** (for shows that
   only have French, e.g. no Spanish but French). Extract the tracks with MKVToolNix:

   ```sh
   mkvmerge -i episode.mkv                                  # list tracks and their numbers
   mkvextract episode.mkv tracks 3:eval/episodes/show/show-e01.en.ass 4:eval/episodes/show/show-e01.es-419.ass
   ```

| Category (`cat`) | EN lines | FR lines |
|---|---|---|
| `dialogue` (normal dialogue in scenes, with context) | 40 | 15 |
| `honorifics` (`-san`, `senpai`, `onii-chan`, `Nee-san`) | 15 | 5 |
| `slang` (`damn it`, `bastard`, `putain`, `mec`) | 15 | 8 |
| `jokes` (puns, English words in Japanese speech) | 8 | 4 |
| `short` (`Huh?`, `Eh ?`, `Tch.`) | 12 | 5 |
| `tags` (`{\i1}…{\i0}`, `{\c&H…&}`, `{\fs}`) | 20 | 8 |
| `breaks` (`\N` and long lines) | 15 | 5 |
| `signs` (simple signs with `\pos`, duplicated layers) | 10 | 3 |
| `songs` (karaoke `\k` that must stay untouched; song translations) | 5 | 2 |
| `glossary` (names and terms) | 10 | 5 |
| **Total** | **150** | **60** |

### manifest.json

```json
{
  "target": "es-419",
  "items": [
    {"id": "lines-en", "source": {"file": "lines/regression.en.jsonl", "lang": "en"}, "tags": ["lines"]},
    {"id": "lines-fr", "source": {"file": "lines/regression.fr.jsonl", "lang": "fr"}, "tags": ["lines"]},
    {"id": "show-e01", "tags": ["episode"],
     "source": {"file": "episodes/show/show-e01.en.ass", "lang": "en"},
     "reference": {"file": "episodes/show/show-e01.es-419.ass", "lang": "es-419"},
     "series": "Show title. One-line synopsis.",
     "glossary": [{"src": "Soul Society", "tgt": "Soul Society"}],
     "from": "1:30", "to": "12:00"}
  ]
}
```

- `id`: letters, digits, `.`, `_`, `-`; used for file names. `tags` let you run a subset
  (`-only episode`).
- The pair is `source.lang` → the reference's language (or the item's `target`, or the
  manifest's `target`, or `es-419`).
- `series` and `glossary` go into the prompt; `from`/`to` limit an episode to a window.
- Episodes are scored against the reference **aligned by time**: lines that overlap by at
  least 30 % of the shorter one are grouped, so one source line split into two reference
  lines (or the other way round) is one segment. Lines without a partner are left out of
  chrF (the count is `aligned_segments`). The official track is not the only valid
  translation: treat chrF as a guide, the blind review decides.

### Line sets (.jsonl)

One JSON object per line, in scene order. `text` and `ref` use ASS markup.

```json
{"id":"en-004","scene":"s1","cat":"tags","who":"Tomo","sec":2.4,"text":"I {\\i1}swear{\\i0} it wasn't my fault!","ref":"¡Te {\\i1}juro{\\i0} que no fue mi culpa!"}
{"id":"en-009","scene":"s2","cat":"songs","style":"OP-Romaji","sec":3.0,"text":"{\\k20}ha{\\k30}ru {\\k25}no","ref":"{\\k20}ha{\\k30}ru {\\k25}no"}
```

`scene` restarts the context; `who` is the speaker; `sec` the duration (for characters per
second); `style` lets you mark songs and signs like an ASS style would.

## Running it

llama-server serves one model at a time, so run the tool once per model. Results accumulate
in `results/`, and every run rebuilds the report from all of them.

```sh
subtranslate-eval run -backend vulkan -machine "desktop 3050 Ti" -cold-load 3.9
subtranslate-eval run -only lines -limit 40         # a quick subset
subtranslate-eval report                            # rebuild report.md / report.json
```

| Flag | Meaning |
|---|---|
| `-endpoint` | server URL (default `http://127.0.0.1:8080`; llama-swap's works too) |
| `-model` | model name to request and report (default: what the server lists) |
| `-preset` / `-presets` | `rapido`, `equilibrado` (default) or `calidad`; or your own presets file |
| `-languages` | extra language registry entries (below) |
| `-signs` | `simple` (default: short signs are translated) or `none` |
| `-only`, `-limit` | item ids or tags to run; max lines per item |
| `-backend`, `-machine`, `-cold-load`, `-gpu` | recorded in the report; `-gpu` is the `nvidia-smi` index for VRAM and temperature |
| `-debug-text` | log prompts and answers (development only; also `SEANIME_SUBTRANSLATE_DEBUG_TEXT=1`) |

Output, under `results/`:

```
report.md, report.json                     per pair and model, per category, per file
runs/<model>__<preset>/<item>.json         metrics of one item (no subtitle text)
runs/<model>__<preset>/<item>.lines.jsonl  source, output and reference per line (for the review)
runs/<model>__<preset>/<item>.translated.ass|srt|jsonl   the translated file
```

## Reading the report

The plan's decision criteria (§3.4):

1. **Quality first:** mean human score ≥ 4.0 (EN) and ≥ 3.8 (FR); severe flags (meaning,
   register) ≤ 5 %; **0 count errors** after retries (`Structure` ok, no fallback caused by
   `missing`); broken tags ≤ 1 % of the lines with tags.
2. **Then speed** (main model only): first chunk of 8 lines ≤ 4 s with the model warm; a
   full episode ≤ 5 minutes (≥ 5× real time).
3. At a tie (< 0.2 in the human score): the more permissive licence and less VRAM.

A prompt or model change that lowers chrF++ by more than 2 points, or raises broken tags or
count errors, doesn't go in. `PROMPT_VERSION` (in `internal/subtranslate/translate`) goes up
with every accepted prompt change.

- **Count errors (1st try)**: items missing from the model's first answer. The JSON schema
  normally makes this 0.
- **Broken tags 1st try %**: of the lines with tags or breaks, how many the model broke the
  first time; **final %** must be 0 (broken lines are retried, then kept in the source).
- **Source-language lines %**: lines still in the source language in the final file
  (fallbacks included).
- **Speed** depends on the backend and the machine. The `Throttled` column says whether the
  GPU slowed down for heat or power during the item (laptops do): ignore those speed numbers.
  The final speed pass is with the two finalists on the server's GPU with CUDA.
- With two slots and continuous batching, llama-server is not bit-for-bit deterministic even
  with a fixed seed: expect chrF to move a few points between identical runs on small sets.

## Blind review

```sh
subtranslate-eval review -lines 80                   # all runs; or -runs a__equilibrado,b__equilibrado
subtranslate-eval review -lines 80 -reference blind  # the official translation as one more hidden candidate
subtranslate-eval review -pair en-es-419             # one language pair
```

It writes `results/review/sheet.csv` and `sheet.md` (the same lines from every model, in
random order, candidates as A, B, C… shuffled on every line, model names hidden) and the key in
`results/review-key.json`, outside the sheet's folder. Don't open the key before scoring.

Fill in `sheet.csv` in a spreadsheet: `score_1_5` per candidate (5 natural and correct, 4 small
style issue, 3 understandable with errors, 2 meaning partly wrong, 1 wrong or unusable) and an
`x` in the flags that apply: `register` (tú/usted, vosotros in es-419), `name_honorific`,
`insult` (censored or exaggerated), `too_long`, `meaning`. Then:

```sh
subtranslate-eval review-score      # per pair and model: mean, distribution, flags; also review-score.md
```

## Languages

Languages are data. `internal/subtranslate/lang/languages.json` has one entry per language or
variant, and the code only reads entries:

| Field | Use |
|---|---|
| `code` | canonical tag: ISO 639-1, plus a region for variants (`es-419`, `es-ES`, `pt-BR`) |
| `short_name` | the plain language name without the variant (`Spanish`), for prompts that expect it (TranslateGemma's) |
| `base` | the canonical key of `internal/torrents/trackprobe` (`es`), the same table the auto-select and the TV use; checked on load |
| `default_for_base` | the variant used when only the language is known (`spa`, `Spanish` → `es-419`) |
| `aliases` | other tags for exactly this variant (`es-mx`, `latino`) |
| `target_rules` | prompt rules when it is the target: register, vocabulary, punctuation (`¿¡` for Spanish, `« »` for French) |
| `source_notes` | prompt notes when it is the source (French: spaces before `? !`, `tu`/`vous`, dropped `ne`) |
| `honorifics_adapt` | equivalents used when honorifics are adapted instead of kept |
| `function_words`, `marks` | frequent words and characters, used to detect lines left in the source language (only the words the two languages of a pair don't share count) |

The system prompt is one template for every pair (`translate/prompt.go`), filled with the
source's notes and the target's rules. **To add a language** (as a source, a target or both),
add an entry: to `languages.json` in a pull request, or for a quick test to a file passed with
`-languages`:

```json
{"languages": [{"code": "ca", "base": "ca", "name": "Catalan", "default_for_base": true,
  "target_rules": ["Natural spoken Catalan. Use 'vosaltres' for plural 'you'."],
  "function_words": ["el", "la", "els", "les", "que", "de", "i", "és", "amb", "per", "una", "però", "això", "molt"]}]}
```

`subtranslate-eval languages` lists the registry. Registered now: `en`, `fr`, `es-419`
(default target), `es-ES`, `pt-BR`, `it`, `de`.

## Presets and models

Presets live in `internal/subtranslate/translate/presets.json` (or a file passed with
`-presets`): batch size, context lines before and after, sampling, honorifics. Defaults
follow the plan: `equilibrado` sends batches of up to 30 lines with the last 6 translated
pairs and the next 3 source lines as context, `temperature 0.2`, `top_p 0.9`, `seed 42`, and
retries once at `0.4`, telling the model which lines failed and why (issue codes, no text).

The same file has **model hints**, matched on the model name and the GGUF file name:

- **TranslateGemma** doesn't do JSON and has a 2K context: it runs in **line mode**, one line
  per request with its own prompt (the one its chat template builds, plus a sentence about
  the tags), validated the same way. Its chat template only accepts structured content
  (`source_lang_code`, `target_lang_code`), so the prompt goes to llama-server's native
  `/completion` endpoint wrapped in Gemma's turn format (`raw_template: "gemma"`).
- **Qwen3 / Qwen3.5 hybrid models** think by default, which wastes tokens and breaks the JSON:
  requests carry `chat_template_kwargs: {"enable_thinking": false}`, any `<think>` block is
  stripped, and the launcher passes `--reasoning off --reasoning-budget 0`. The budget matters:
  some GGUFs' templates (seen with a Qwen3.5-4B GGUF) always open `<think>` and ignore
  `enable_thinking`, so without it the model thinks until it runs out of tokens and the answer
  is empty (the tool then reports "the model is probably thinking"). Qwen3 **Instruct-2507**
  has no thinking mode.

Batch mode asks for `{"id":"…","t":[[id,"text"],…]}` with a strict JSON schema that pins the
ids and the count, which llama-server turns into a grammar.

## Privacy

- The eval set and the results stay outside the repository; `.gitignore` and
  `TestNoEvalDataInRepo` guard against them being committed. Tests use invented lines only.
- Logs never contain subtitle text, prompts or answers unless `-debug-text` is set; server
  error messages (which can quote the prompt) are only logged with it too. The result JSON
  files have metrics only; the `.lines.jsonl`, translated files and review sheets have text and
  stay in the results folder.
- Everything runs locally against your own llama-server.

## Code layout and limits of F1

| Package | Contents |
|---|---|
| `internal/subtranslate/lang` | language registry and the source/target detector |
| `internal/subtranslate/markers` | placeholders `⟦n⟧`/`⟦N⟧`, restore and checks |
| `internal/subtranslate/ass` | lossless ASS/SSA reader and writer (byte-for-byte when unchanged), tag tokenizer, event classifier (dialogue, sign, song, karaoke, drawing, effect) |
| `internal/subtranslate/srt` | SRT reader and writer |
| `internal/subtranslate/translate` | prompt, OpenAI-compatible client (llama-server timings, `/props`, `/metrics`), batches with context, validation, one retry, presets |
| `internal/subtranslate/eval` | chrF/chrF++, alignment, runs, reports, blind review |
| `cmd/subtranslate-eval` | the CLI |
| `scripts/subtranslate-llama-server.sh` | starts llama-server for one model with the plan's flags |

Left for phase F2 (the translation core): the rest of the retry ladder (split batch, line by
line, without inner tags), line wrapping at 42 characters, fonts without `ñ`/`¿`, the glossary
memory, song/sign options beyond "skip songs, translate simple signs", and the output header.
