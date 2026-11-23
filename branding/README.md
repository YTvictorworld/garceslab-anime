# Branding

`logo.svg` is the single source of the Garceslab Anime logo: a placeholder "GA" monogram
drawn with plain strokes (no fonts, no third-party artwork), licensed GPL-3.0 like the rest
of the repository. To change the logo, edit or replace `logo.svg` and run:

```sh
sh branding/export.sh   # needs rsvg-convert and ImageMagick 7
```

It rewrites the web UI logo (`seanime-web/public/garceslab-logo.png`), the favicons and PWA
icons (`seanime-web/public/icons/`), and the server's embedded logo and Windows icon
(`internal/icon/`). The TV app has its own copy and script in
[garceslab-anime-tv](https://github.com/YTvictorworld/garceslab-anime-tv/tree/main/branding).
