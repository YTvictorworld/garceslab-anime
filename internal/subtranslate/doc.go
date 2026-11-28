// Package subtranslate is the self-hosted subtitle translation of Garceslab Anime:
// subtitles of a release are translated by a local LLM (llama.cpp's llama-server)
// when no release has the profile's language.
//
// Phase F1 (this code so far) is the evaluation tool cmd/subtranslate-eval and the
// parts of the core it needs, in their final places:
//
//	lang/       language registry (languages.json): codes, prompt rules, detection
//	markers/    placeholders that protect formatting tags from the model
//	ass/        lossless ASS/SSA reader and writer, tag tokenizer, event classifier
//	srt/        SRT reader and writer
//	translate/  batches, prompt, OpenAI-compatible client, validation, retry, presets
//	eval/       metrics (chrF/chrF++, blocking checks, speed), reports, blind review
//
// See docs/subtitle-translation-eval.md.
package subtranslate
