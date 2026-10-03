# MemeShorts

Z meme šablon dělá 6–15s vertikální shorty (1080×1920, H.264, 30 fps, AAC, −14 LUFS)
s **vlastními postavami**. Originál memu je jen předloha struktury vtipu, do výstupu
se nikdy nedostane. Plán: [`Prompts/00-MEME_SHORTS_PLAN.md`](Prompts/00-MEME_SHORTS_PLAN.md).

## Rychlý start

```bash
make build
./bin/memeshorts ingest                       # ~100 šablon z Imgflip → data/memes.db
./bin/memeshorts list                         # ★ = známá šablona
./bin/memeshorts run --meme imgflip:181913649 --recipe kenburns \
    --text "Writing unit tests" --text "Testing in production"
# → jobs/imgflip-181913649-kenburns/compose/short.mp4
```

Bez `--text` napíše vtip LLM (přes `llm.ol1n.com` potřebuje `CF_ACCESS_CLIENT_ID/SECRET` v `.env`, viz
`.env.example`). Bez schválené knihovny postav se použijí barevné placeholdery, takže roura běží celá i offline.

## Postavy: knihovna místo generování na short

Postavu neudrží prompt ani seed sám o sobě (G0 v `Prompts/01-GRAPHICS_PLAN.md`), proto se obrázky
postav negenerují pro každý short, ale jednou jako schválená knihovna *postava × emoce*:

```bash
./bin/memeshorts library masters            # kandidáti předloh (flux-schnell NIM + Animagine), drafts/library/
./bin/memeshorts library pick kiro pixar master kiro/master_pixar_3.png
./bin/memeshorts library emotions           # 12 emocí z vybraných předloh (obě linie)
./bin/memeshorts library pick kiro pixar smug kiro/pixar_smug_v1_0.png
./bin/memeshorts library cutout             # výřezy do alfy (BiRefNet)
./bin/memeshorts library approve            # → assets/characters/<postava>/<linie>/<emoce>.png + provenance
open drafts/library/sheet.html              # kontaktní arch pro výběr
```

Dvě linie: `pixar` (flux-schnell NIM, prompt + seed předlohy; jede 07–13 i 19–01) a `anime`
(Animagine XL + IP-Adapter Plus, jen ComfyUI 07–13). Slovník emocí a aliasy: `assets/characters/emotions.yaml`.
`run` pak bere obrázky z knihovny (`--gen library`, `--line pixar|anime`); bez schválené knihovny
spadne na placeholdery. SPARK se volá přímo v LAN (`192.168.88.66`), mimo okna klient nic neposílá.

## Roura

`classify → storyboard → generate → animate → compose → qa`, každý stage pod
`jobs/<id>/<stage>/` a v `manifest.json`. Klíč stage = hash jeho voleb + klíč
předchozího, takže opakovaný běh je zadarmo a změna textu přepočítá jen to, co musí.
`--redo-from <stage>` vynutí přepočet (u `classify` si řekne LLM o nový vtip).

| Stage | Co dělá |
|---|---|
| classify | ruční `--text` > cache u memu > LLM (JSON schéma + 1 retry s chybou validace). `safe_for_brand=false` / `needs_context=true` → skip (`--force`) |
| storyboard | role A–D → postavy z `assets/characters/map.yaml`, délky dle čtení, 6–15 s v celých snímcích |
| generate | `library` (schválené obrázky postav, emoce přes aliasy → 12 knihovních), `genqueue` (flux-schnell txt2img — postavu nedrží, jen pro testy) nebo `placeholder` |
| animate | recept `kenburns` (zoompan; první scéna má 0.8s hook zoom, punchline prudký nájezd) |
| compose | postava na výšku přes spodek plátna (1080×1578), blur pozadí, titulky (renderované v Go → PNG, bez závislosti na drawtext), AI badge, whoosh/boom SFX, dvouprůchodový loudnorm, AI metadata |
| qa | délka, rozlišení, kodeky, fps, audio, black frames, loudness ±1.5 LU. OCR titulků zatím přeskočeno |

SFX se při prvním běhu syntetizují ffmpegem do `assets/sfx/` (licenčně čisté);
vlastní `boom.wav`/`whoosh.wav` mají přednost. Podkres: první soubor v `assets/music/`.

## Stav vůči milníkům

- **M1** — ingest (Imgflip; Reddit za `MEMESHORTS_REDDIT=1`), classify, storyboard,
  `kenburns`, compose, qa: hotovo, 1 příkaz = 1 short.
- **Grafika** (`Prompts/01-GRAPHICS_PLAN.md`): G0 test hotový, G1 knihovna postav ve výrobě (`library`).
  Dál: noční pozadí k tématu (flux-schnell), recept `parallax`, knihovna pohybu (Wan 2.2, ~5 min/klip).
- Chybí: OCR pro Reddit memy (texty), napojení na AiStack job queue, recepty `i2v`/`dialog`/`panels`,
  TTS (na SPARKu od 2. 10., česky s vlastním hlasem komerčně nejde), publish, metrics.

## Testy

`make test` (e2e test potřebuje ffmpeg), `make test-short` bez něj.
