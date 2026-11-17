# GRAPHICS_PLAN.md — grafika shortů: postavy, pozadí, pohyb

Navazuje na `00-MEME_SHORTS_PLAN.md` (§3.4 Generate, §3.5 Animate). Stav 2. 10. 2026.
Možnosti SPARKu jsou domluvené se session **Director**; zdroje: AiStack `docs/IMAGE-GEN-GUIDE.md`
(origin/main), `PLAN-spark-scheduler.md` §3, `storyteller/STORYTELLER_CHARACTER_MODELS.md`,
`video-stack/ANIME_STORY_PLAN.md`. „Neověřeno" = nikdo to nezměřil; rozhodne fáze G0.

---

## 0. Co SPARK umí a co z toho plyne

| Fakt (Director) | Důsledek pro MemeShorts |
|---|---|
| **flux-schnell NIM** (gen-queue `<spark-host>:8091`) běží 07–13 a 19–01, ~3–6 s/obrázek, Apache-2.0 | jediný backend dostupný večer a jediný licenčně čistý → denní dávka musí stát na něm |
| **ComfyUI** (`<spark-host>:8188`) jen 07–13 a primárně pro experimenty uživatele | vše, co potřebuje referenci (img2img, Kontext, IP-Adapter, RMBG, video), je vzácný zdroj → dělat jednorázově, ne denně |
| flux-kontext NIM a image-api **nejsou nasazené** | Kontext jen přes ComfyUI; plán §3.4 („IP-Adapter / face-avatar pipeline") v původní podobě neplatí |
| Prompt + seed postavu **nedrží** (0–35 % nad branou DINO 0,80); img2img / Kontext z reference ano (85–100 %) | dnešní `generate` (flux-schnell txt2img se seedem per postava) dá v každé scéně jinou postavu → nepoužitelné pro stálé obsazení |
| Wan 2.2 ti2v 5B: ~25 min / 121 snímků, bez checkpointu; 10–30 klipů denně se do okna **nevejde** | recept `i2v` per short (plán §3.5, M2) v plánovaném objemu nejde |
| TTS na SPARKu zatím neběží (nasazení 2. 10. večer); česky s vlastním hlasem postavy komerčně nejde | mimo grafiku, týká se receptu `dialog` (M3) — viz §8 |
| Z Macu v LAN se volá přímo IP, bez CF Access; na SPARKu nic nespouštět, jen HTTP; dávky hlásit Directorovi | `.env` default změnit na LAN adresy |

**Hlavní rozhodnutí: knihovna místo generování na short.** Postavy v memech dělají pořád totéž —
tváří se. Proto se obrázky ani klipy postav negenerují pro každý short, ale **jednou** jako
schválená knihovna *postava × emoce*. Denní provoz pak z knihovny jen vybírá a přes noc si
ve flux-schnell dogeneruje pozadí k tématu vtipu. Tím odpadá závislost denní dávky na ComfyUI,
identita je daná výběrem (ne štěstím) a licence se řeší jednou.

---

## 1. Vrstvy obrazu

| Vrstva | Odkud | Kdy vzniká | Backend |
|---|---|---|---|
| **Postava** (still) | knihovna `assets/characters/<postava>/emotions/<emoce>.png` | jednorázově (G1) | ComfyUI, 07–13 |
| **Postava** (pohyb) | knihovna `…/motion/<emoce>.mp4`, smyčka 3–5 s | jednorázově, po částech (G3) | ComfyUI Wan 2.2, 07–13 |
| **Pozadí** k tématu vtipu | `jobs/<id>/generate/bg_NN.jpg` | denně, noční dávka | flux-schnell NIM, 19–01 |
| **Titulky, badge** | Go renderer (hotovo) | při compose | CPU |
| **Značka** (logo, end card, paleta) | `assets/brand/` | jednorázově (G4) | flux-schnell + ruční výběr |

Formát: postavy **na výšku 832×1216** (Wan i SDXL jsou na něj trénované), ne čtverec 1024² jako dnes.
Compose pak dá postavu přes celé plátno místo čtverce v rozmazaném rámu.

---

## 2. Knihovna postav

### 2.1 Slovník emocí
Šablony dnes používají 33 různých emocí (smug 8×, většina 1×). Knihovna jich má **12**; ostatní se
mapují na nejbližší (`assets/characters/emotions.yaml`) a LLM v classify smí vybírat jen z nich
(enum ve schématu).

`neutral · smug · pleased · disgusted · shocked · confused · angry · nervous · exhausted · scheming · proud · deadpan`

4 postavy × 12 emocí = **48 obrázků**. Při 3–4 variantách na výběr ~170 generací.

### 2.2 Postup (dva kroky, oba se schválením)
1. **Master** — pro každou postavu 6–8 návrhů txt2img (celá horní půlka těla zepředu, ploché
   jednobarevné pozadí, žádný text). Uživatel vybere jeden → `master.png`. Je to jediná reference;
   vícepohledový arch ne (Kontext by ho kopíroval do výstupu — zkušenost z video-stack).
2. **Emoce** — z masteru přes referenci, prompt mění jen výraz a gesto. 3–4 varianty na emoci,
   kontaktní arch (HTML) → uživatel vybere. Brána: DINOv2 ≥ 0,80 k masteru (nástroj je v Ol1nLLM
   `tools/lab`), pod bránou se varianta do archu nedostane.

### 2.3 Čím generovat — kandidáti pro G0
| | Postup | Drží postavu | Licence | Pozn. |
|---|---|---|---|---|
| **A** | flux **Kontext** v ComfyUI („same character, now disgusted, arms crossed") | 90 % (měřeno, flux-dev img2img) | FLUX.1 [dev] Non-Commercial — **ověřit** | ~45 s/obr.; workflow `video-stack/workflows/story_keyframe_kontext.json` |
| **B** | anime SDXL (Animagine 4 / Hassaku / NoobAI) img2img + IP-Adapter Plus + openpose | 100 % img2img bez stylu (měřeno na 8 postavách) | vlastní licence každého modelu — **ověřit** | nejlepší anime vzhled; 10–20 s (neověřeno); workflow `story_keyframe_ipadapter.json` |
| **C** | flux-**schnell** v ComfyUI, img2img 0,5–0,8 (12 kroků kvůli jemnému denoise) | 100 % (měřeno) | Apache-2.0 | jisté licenčně; **neověřeno**, jestli img2img změní výraz dost — drží i pózu |
| D | LoRA na postavu | neověřeno | podle základu | zeptat se session FineTune; až kdyby A–C nestačily |

Illustrious 2.0 vyšel v labu z měřených nejhůř (75–85 %), proto v B není první volbou.

Kanál má být monetizovaný → **do produkční knihovny jen model s ověřenou licencí**. C je jisté,
A a B potřebují přečíst licence (u FLUX dev zvlášť podmínky pro použití výstupů, ne jen modelu).

### 2.4 Výřez postavy
Mastery i emoce se generují na plochém pozadí a vyříznou do alfy (`<emoce>_cut.png`):
ComfyUI-RMBG / BiRefNet (nainstalované, **neměřené**), záloha klíčování barvy ve ffmpeg.
Výřez umožní vlastní pozadí ke každému vtipu a recept `parallax` (§4).

---

## 3. Pozadí (denní dávka)

- Storyboard dostane pole `setting` (kancelář, třída, herna, kuchyně v noci…) — doplní LLM v classify.
- Noční dávka 19–01: pro každý short 1 pozadí × 2 varianty ve flux-schnell NIM, 832×1216
  (obdélník v NIM **neověřen** — násobky 64; záloha 1024² + ořez). 10 shortů ≈ 20–40 obrázků ≈ minuty.
- Prompt bez slov „poster/book/label/sign"; flux-schnell „no text" ignoruje a u ~40 % obrázkových
  promptů dokreslí falešné popisky. Kontrola OCR (vzor: storyteller `tools/find-lettering.swift`),
  při nálezu druhá varianta.
- Cache podle `setting`: stejné prostředí se nedělá znovu, po pár týdnech je většina pozadí z cache.
- Pravidla gen-queue: souběžnost 2, na `done` čekat minuty, výsledek je **JPEG** a drží se 1 h, seed < 2³².

---

## 4. Pohyb — recepty podle ceny

| Recept | Vstup | GPU | Stav |
|---|---|---|---|
| `kenburns` | still postavy | 0 | hotovo |
| `parallax` | výřez postavy + pozadí; vrstvy se hýbou různou rychlostí, pop-in postavy, třes na punchline | 0 | nový, hned po G1 |
| `loop` | klip z knihovny pohybu (dýchá, mrkne, výraz naběhne), opakovaný do délky scény | 0 při renderu | po G3 |
| `i2v` na míru | Wan 2.2 pro konkrétní short | 25 min/klip (ti2v 5B, měřeno) | jen výjimečně, max 2–3 denně v okně 07–13 |

**Knihovna pohybu (G3):** 48 smyček se při 25 min/klip nevejde (20 h GPU). Proto:
- nejdřív změřit 3 klipy: Wan 2.2 i2v 14B + Lightning LoRA (4 kroky) na 832×1216 draftu —
  video-stack uvádí ~150 s na 81 snímků, Director to potvrzené nemá (**neověřeno**);
- vyjde-li ≤ 5 min/klip → celá knihovna (48 klipů ≈ 2–4 h, dvě rána);
- vyjde-li ~25 min → jen 16 nejpoužívanějších kombinací (≈ 7 h, po kouscích), zbytek `parallax`.

Anime styl ve Wan: `style_tail` „anime style, 2D animation, cel shading, clean lineart, flat colors,
static camera, one continuous movement", negative „3d, realistic, photo". Identitu přes FaceDetailer/PuLID
**nepoužívat** (InsightFace anime tváře nedetekuje). Dvě postavy v jednom záběru Wan míchá →
dialog vždy střihem, jak to storyboard už dělá. Video job nemá checkpoint: nespouštět méně než
40 min před 13:00.

---

## 5. Značka (G4)

Název a logo kanálu, end card (0,5 s), barva na postavu (už v `map.yaml`) pro jmenovky a zvýraznění
titulků, ikona kanálu, banner. Font Anton zůstává. AI badge zůstává. Vše flux-schnell + ruční výběr,
jednorázově. Náhledový snímek = snímek z punchline scény (QA ověří, že v něm titulek je celý).

---

## 6. Fáze

| Fáze | Co | Backend / okno | GPU čas | Výstup |
|---|---|---|---|---|
| **G0** | Test stylu a identity: 1 postava, master + 4 emoce, postupy A, B, C. Měření časů, DINO, test RMBG, test 832×1216 v NIM, 3 zkušební Wan klipy | ComfyUI 07–13 | ~1,5 h | kontaktní arch + tabulka; **uživatel vybere postup a styl** |
| **G1** | Mastery 4 postav → schválení → 48 emocí (×3–4 varianty) → schválení → výřezy | ComfyUI 07–13, dvě rána | ~1–2,5 h | `assets/characters/**`, provenance |
| **G2** | Kód: provider `library` (místo txt2img), `setting` + noční dávka pozadí, recept `parallax`, compose přes celé plátno, OCR kontrola pozadí | flux-schnell 19–01 | minuty denně | short bez placeholderů, denní provoz bez ComfyUI |
| **G3** | Knihovna pohybu + recept `loop` | ComfyUI 07–13, po částech | 2–7 h podle měření | `motion/*.mp4` |
| **G4** | Značka, end card | flux-schnell | minuty | `assets/brand/` |

Před každou dávkou zpráva Directorovi (čas, počet). G0 a G1 berou okno, které je primárně
uživatelovo → termín určí uživatel.

### Úpravy stávajícího kódu (součást G2, drobnosti hned)
- `generate/genqueue.go`: výsledek je JPEG, klient dnes vyžaduje PNG → dekódovat a uložit jako PNG.
- `config`: default `GEN_QUEUE_URL=http://<spark-host>:8091/nim`, CF Access jen volitelně.
- `generate`: nový `ComfyClient` (`POST /prompt`, `/history`, `/view`, `/upload/image`; vzor storyteller
  `internal/comfy`) — používá ho jen stavba knihovny, ne denní roura.
- `classify`: emoce jako enum 12 hodnot + `setting`; `templates.go` přemapovat.
- `storyboard`/`compose`: 832×1216, vrstvy postava + pozadí.
- Kontrola okna: mimo 07–13 / 19–01 job do fronty nepouštět, ohlásit a skončit.

### Provenance
Ke každému assetu `*.json`: model, workflow, prompt, seed, datum, licence modelu. Slouží pro licenční
audit (monetizace) a AI Act. Drafty v `drafts/` (gitignored), do `assets/` jen schválené.

---

## 7. Rozhodnutí uživatele (2. 10. 2026)

1. **Styl a licence:** v G0 zkusit obojí, **primárně flux-schnell** (rychlý, licenčně čistý).
2. **Postavy:** čtyři nové (Kiro, Mei, Taro, Yuki z `map.yaml`).
3. **Objem videa:** knihovna smyček místo klipu na každý short — schváleno.
4. **Okno 07–13:** k dispozici celé.

### G0 — co se měří (`memeshorts lab g0` → `drafts/g0/sheet.html`)
Postava Kiro, emoce smug / disgusted / shocked / exhausted, dvě varianty na buňku.

| Postup | Backend | Co |
|---|---|---|
| **S** | flux-schnell NIM | txt2img se seedem masteru, mění se jen popis výrazu (jde i večer, bez ComfyUI) |
| **C4 / C12 / C12hi** | flux-schnell v ComfyUI | img2img z masteru: 4 kroky 0,8; 12 kroků 0,7; 12 kroků 0,85 |
| **A** | FLUX Kontext | úprava výrazu a pózy z masteru, 20 kroků |
| **B1 / B2** | anime SDXL (Animagine 4) | img2img 0,65 / IP-Adapter Plus 0,8 z anime masteru |
| výřez | ComfyUI-RMBG | mastery do alfy |
| pohyb | Wan 2.2 i2v 14B Lightning | 592×864 × 49 a 81 snímků, 832×1216 × 49 snímků |

Mastery flux-schnell ve dvou stylech (anime, pixar-3d), 4 seedy. Identita: DINOv2 k masteru
(`memeshorts lab dino`), rozhoduje pohled do archu.

## 7a. Výsledky G0 (2. 10. 2026, 07:04–08:20, okno comfy)

Arch: `drafts/g0/sheet.html`, data `drafts/g0/results.json`. Postava Kiro, 4 emoce × 2 varianty na postup.
DINO = DINOv2 podobnost celého obrázku k masteru (brána 0,80); změna pózy ji snižuje i u správné postavy.

| Postup | s/obr. | DINO ø (nad branou) | Co je vidět |
|---|---|---|---|
| **S** flux-schnell NIM, txt2img se **seedem masteru**, pixar-3d | ~10 (s frontou) | 0,90 se seedem masteru | drží postavu **a** mění výraz i gesta; s jiným seedem jiná postava; „disgusted" vyšel jako naštvaný — ladit prompt |
| S, anime styl | ~10 | 0,84 (6/8) | drží, ale styl je obecný, slabší než Animagine |
| C flux-schnell img2img (4 / 12 kroků) | 15–35 | 0,96–0,98 (8/8) | postava stejná, **výraz se skoro nezmění** — nepoužitelné |
| A FLUX Kontext z masteru | 74–84 | 0,86 (7/8) | nejlepší anime: postava drží, výrazy i gesta silné; mění barvu pozadí; licence neověřena |
| B1 Animagine img2img 0,65 | 11 | 0,98 (8/8) | postava dokonale, mění jen obličej, póza zůstává |
| B2 Animagine + IP-Adapter Plus | 13 | 0,83 (6/8) | dostane i gesta, ale bledne barva a mění se účes |

Další měření:
- **NIM 832×1216 funguje** (dosud neověřeno).
- **Výřez** BiRefNet (obecný i toonout): ~4 s/obr., čistý okraj na anime i 3D stylu. RMBG-2.0 nepoužívat (nekomerční).
- **Wan 2.2 i2v 14B + Lightning (4 kroky):** 592×864 × 49 sn. 204 s; × 81 sn. 304 s; 832×1216 × 49 sn. 370 s.
  Tedy **3,5–6 min na klip** (ne 25 min). Z neutrálního masteru umí rovnou přejít do emoce včetně gesta
  (shocked: ruce na tváře), postava drží — ověřeno na anime i pixar-3d masteru (pixar: 81 sn. 303 s,
  49 sn. 201 s). Knihovna 48 klipů ≈ 4 h = jedno okno.
- Flux-schnell anime mastery jsou slabší než Animagine; flux-schnell pixar-3d mastery jsou čisté a výrazné.

**Důsledky pro plán:**
- Primární cesta (rozhodnutí uživatele: flux-schnell) = **styl pixar-3d, postup S**: master = prompt + seed,
  emoce = stejný seed + jiný popis výrazu. Jede přes NIM v obou oknech, ComfyUI nepotřebuje, licence Apache-2.0.
  Varianty na výběr nevznikají jiným seedem, ale obměnou formulace výrazu.
- §2.3 postup C vypadává. Anime záloha: master Animagine + B1 pro obličej, případně Kontext pro gesta
  (obojí po ověření licence).
- §4: knihovna pohybu se vejde celá; klip „neutrál → emoce" dá zároveň náběh výrazu, takže recept `loop`
  = přehrát klip jednou a podržet poslední snímek.

## 7b. Stav G1 (3. 10. 2026, 12:40)

Rozhodnutí uživatele po G0 (přes Directora): dvě linie — **S** (flux-schnell NIM, seed předlohy, pixar-3d)
a **B2** (Animagine XL + IP-Adapter Plus, anime). Video a hlas zatím nerozhodnuty.

- `memeshorts library` (masters / emotions / cutout / pick / approve), arch `drafts/library/sheet.html`,
  výběr `drafts/library/selection.yaml`, slovník emocí `assets/characters/emotions.yaml`.
- Předlohy: 8 kandidátů × 4 postavy × 2 linie hotové; předběžný výběr je můj, uživatel ho může změnit
  (`library pick <postava> <linie> master <soubor>`; emoce linie S pak přegenerovat, protože sdílí seed).
- Emoce B2 (anime): hotové pro všechny 4 postavy (12 × 2 varianty). IP-Adapter je ~70 s na job
  (2 obr.), ne 13 s jako v G0 — Kiro 17 min, celkem ~65 min ComfyUI.
- Emoce S (pixar): Kiro hotový (35 obr.), Mei 6/35; zbytek běží 3. 10. v 19:17 (NIM okno).
- Výřezy: zatím ne (ComfyUI, zítra v okně).
- Pozorování: linie S drží postavu přes všech 36 obrázků; „disgusted" flux-schnell kreslí spíš jako vztek
  → upravit formulace v emotions.yaml a přegenerovat jen tu emoci (`--emotion disgusted`). IP-Adapter
  bledne barvy oblečení (červená → lososová) a u Mei dělá neprůhledné brýle.
- Roura: `run` bere obrázky z knihovny (`--gen library`, `--line`), postava na výšku přes spodek plátna
  (1080×1578), titulek v pásu 110–500 px. Ověřeno zkušebním renderem.

## 8. Co plán nepokrývá
- **TTS** (stav 2. 10., Director): hotové v AiStack PR #18, nasazení na SPARK dnes v okně 19–01 —
  **zatím neběží**. API přes gateway `/v1/audio`: `POST /v1/audio/tts` → job,
  `GET /v1/audio/voices?commercial=true&lang=…`, uložené hlasy postav `POST /v1/audio/voices/{id}`
  (klonování), OpenAI shim `POST /v1/audio/speech`. Nekomerční modely API ve výchozím stavu odmítá (403).
  Původní plán §0 („Kokoro EN/CZ, fallback XTTS") neplatí:

  | Model | Česky | Vlastní hlas postavy (klon) | Komerčně |
  |---|---|---|---|
  | Kokoro | ne | ne | ano |
  | Chatterbox | ne | ano (s vodoznakem) | ano (MIT) |
  | Piper | ano | ne | jen hlas Kasandra (CC BY, uvést autora) |
  | XTTS | ano | ano | ne |
  | chatterbox-cs (komunitní) | ano | ano | licence neověřena |

  **Rozhodnutí pro uživatele (recept `dialog`, M3):** česky s vlastním hlasem každé postavy to
  komerčně zatím nejde. Možnosti: (a) kanál s hlasem jen anglicky — Chatterbox s klonem na postavu,
  nebo Kokoro; (b) česky jedním společným hlasem Kasandra s uvedením autora; (c) česky bez hlasu,
  jen titulky a SFX jako dnes. Odpovídá to otevřené otázce 2 původního plánu (EN vs. CZ kanál).
- **CF Access token** nikdo nemá; mimo LAN roura nepoběží, dokud ho uživatel nevystaví.
- Publish, metriky — beze změny podle původního plánu.
