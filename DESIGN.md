# DESIGN.md — Smart Campaign Composer görsel sistemi

Bu dosya arayüzün görsel dilini, bileşen envanterini ve ekran davranışını tanımlar. **Bileşen adları, prop'lar, `data-testid`'ler, API uç noktaları ve SSE olay adları burada tanımlanmaz**; onların tek kaynağı `docs/contracts/**` (özellikle `docs/contracts/ui.md`). Burada geçen bileşen adları (`BriefComposer` gibi) çalışma adıdır; sözleşmeyle çelişirse sözleşme kazanır.

Etiketler: **sampled** = referans görselden piksel ölçümüyle alındı · **approximated** = göz/türetme ile seçildi · **VARSAYIM** = projenin kararı, `DECISIONS.md`'ye taşınır · **ÖNERİ** = mühendise öneri, bağlayıcı değil · **DOĞRULANMADI** = resmi kaynaktan teyit edilemedi.

---

## 1. Amaç ve ilkeler

Pazarlamacı ne istediğini yazar; sistem kitleyi ve metni önerir; son söz insanındır. Arayüz bu güven ilişkisini taşır.

1. **Sakin özgüven.** Sıcak, açık bir zemin; az sayıda ama net vurgu. Turuncu "burası önemli" demek içindir, süs için değil. Ekranda aynı anda en fazla bir birincil (gradient) buton bulunur.
2. **İnsan kontrolde.** Claude'un ürettiği her şey önce **taslak** görünür; `validated` gelmeden onay mümkün değildir (REQ-CNT-04, REQ-APR-01). Onay, gönderim gibi geri alınamaz adımlar onay diyaloğundan geçer.
3. **Durum her zaman görünür.** Kampanya durumu, akışın durumu (akıyor / doğrulandı / geçersiz / kesildi), LLM sağlayıcısı (REQ-AI-03) ve teslimat sayaçları (REQ-TRK-01) ekranda her an okunur; hiçbir durum yalnızca renkle anlatılmaz.
4. **Sayılar birinci sınıf.** Kitle sayısı, karakter sayacı, teslimat sayaçları büyük, tabular rakamlarla ve marka turuncusuyla gösterilir; referanslardaki "anahtar ifade turuncu" dili burada sayılara taşınır.
5. **LLM metni veridir, işaretleme değil.** LLM veya kullanıcı metni hiçbir yerde HTML olarak yorumlanmaz (REQ-UI-01); bu kural görsel tasarımın sınırını da çizer (zengin metin editörü yok).

## 2. Kaynaklar

Referanslar `assets/InsiderOne.jpeg` ve `assets/InsiderOne2.jpeg` (800×800; üçüncü taraf pazarlama görselleri olduğu için repoda yoktur, `.gitignore`'dadır). Renkler Python + Pillow 11.3 ile 3×3 piksel ortalaması veya bölgesel renk modu alınarak ölçüldü; JPEG sıkıştırması nedeniyle ±4 birim sapma beklenir. Pazarlama görsellerinden yalnızca palet, tipografi karakteri ve biçim dili türetildi; görsellerin kendisi, ikonlar ve pazarlama metni alınmadı. **İstisna (sahip kararı, 2026-10-27):** `assets/InsiderOneLogo.jpeg` yalnızca şu yerlerde kullanılır: uygulama başlığı, favicon/sekme ikonu, boş ve yükleniyor durumları (düşük kontrastlı küçük işaret), onay sayfası özet kartı (dekoratif rozet) ve email önizleme çerçevesinin üst şeridi (sandbox'lı iframe'in dışında; `srcdoc` içine konmaz). Dosya `web/public/brand/insider-one-logo.jpeg` olarak repodadır (`assets/` klasörü `.gitignore`'da olduğu için o adla bir klasör kullanılamaz); favicon PNG'leri (`favicon-16.png`, `favicon-32.png`) aynı dosyadan `sips` ile türetilmiştir. `assets/` altındaki diğer görseller kullanılmaz. Uygulamanın kendi wordmark'ı "Smart Campaign Composer" logonun yanında kalır.

| Öğe | Görsel 1 (sohbet + çubuklar) | Görsel 2 (canlı durum kartı) | Bizde nereye gitti |
|---|---|---|---|
| Sayfa zemini | üst `#EFDBD2`, orta `#F0E3DB`, alt `#EFEAE4` (sampled) — dikey, pembe-şeftaliden kırık beyaza | üst `#E9E4DE`, orta `#E1D9D7`, alt `#EFEAE4` (sampled) | `--gradient-page` |
| Kart yüzeyi | `#FCFCF9`, başlık şeridi `#FCFCFA`, ayraç `#F6F6F4` (sampled) | `#F9FAF8` (sampled) | `--surface-card`, `--border-subtle` |
| Kullanıcı balonu | `#F4DDD7`, `#F2E1DA` (sampled), metni `#5C4848` civarı (sampled) | — | `--surface-bubble-user` |
| Başlık vurgusu | 2. satır turuncu-kırmızı `#EC3430` (sampled, mod) | 1. satır turuncu `#E8582E` (sampled, mod) | `--orange-500` ailesi, metin politikası §4 |
| Başlık mürekkebi | `#241C20` / `#281820` (sampled) | `#241C20` (sampled) | `--ink-900` |
| Gövde metni | `#3C2C2C` (sampled) | — | `--ink-700` |
| İkincil metin | `%` etiketleri ve dipnot `#B0B0B0` (sampled) — **kontrast yetersiz, kopyalanmadı** | — | `--ink-500` daha koyu seçildi |
| Çubuklar | dolgu `#F4452B` (1.), `#F3967F` (2.), `#F1C7BA` (3.); iz `#EFEFED` (sampled); kalın ~8 px, tam yuvarlak uç | dolgu `#F66850`, iz `#CEC5CE` (sampled); ~8 px | `ProgressBar`, `--track` |
| CTA | pill, dolgu `#F64724`–`#F85627` (sampled) | dikey gradient üst `#FA5B23` → alt `#EA2D2B` (sampled); beyaz metin + ↗ ok | `--gradient-brand-vivid`; AA için `--gradient-cta` |
| Şekil | kart köşesi ~12–16 px, balon köşesi ~10 px, çok yumuşak gölge (approximated) | kart ~20 px, belirgin ama dağınık gölge (approximated) | §5 |
| Tipografi | geometrik-hümanist grotesk; çift katlı `a`, yuvarlak `o`, düz kuyruklu `y`; başlıklar Regular/Medium, büyük boy | aynı aile | §4 (Figtree, approximated) |

**Font tespiti (approximated):** Referanslardaki yazı ailesi kesin teşhis edilemedi; karakteri açık iç boşluklu, düşük kontrastlı, geometrik-hümanist bir grotesk. Ücretsiz en yakın eşleşme **Figtree** (Google Fonts, değişken 300–900). Yedek aday: Hanken Grotesk.

## 3. Renk token'ları

Tüm değerler CSS değişkeni olarak `:root`'ta tanımlanır (§6). Light tema zorunlu; dark tema opsiyonel (P6'da zaman kalırsa), token adları paraleldir.

### 3.1 Turuncu (marka) skalası

| Token | Light | Kaynak | Kullanım |
|---|---|---|---|
| `--orange-50` | `#FDF1EC` | approximated | seçili satır, odak halkası iç zemini |
| `--orange-100` | `#F4DDD7` | sampled (balon) | kullanıcı balonu, chip zemini |
| `--orange-200` | `#F1C7BA` | sampled (3. çubuk) | çubuk 3. seviye, near-limit iz |
| `--orange-300` | `#F3967F` | sampled (2. çubuk) | çubuk 2. seviye, hover çizgisi |
| `--orange-400` | `#F66850` | sampled (görsel 2 çubuk) | dark tema ekran metni |
| `--orange-500` | `#F4462A` | sampled (1. çubuk / CTA) | **parlak marka**: grafik, ikon, çubuk, büyük metin (yalnızca kart/beyaz üstünde) |
| `--orange-600` | `#D63A1E` | approximated (500'ün koyusu) | büyük metin her açık zeminde; normal metin kart/beyaz üstünde; CTA gradient üst |
| `--orange-700` | `#B83015` | approximated | **küçük metin her açık zeminde** (link, etiket, sayaç) |
| `--orange-800` | `#9C2812` | approximated | link hover/pressed |
| `--red-cta-end` | `#C42A26` | approximated (`#EA2D2B` koyusu) | CTA gradient alt |

### 3.2 Mürekkep / nötr skala (sıcak, hafif mor-kahve tonlu)

| Token | Light | Kaynak | Kullanım |
|---|---|---|---|
| `--ink-900` | `#241C20` | sampled | başlık, input metni |
| `--ink-700` | `#3C2C2C` | sampled | gövde metni |
| `--ink-600` | `#5E5054` | approximated (`#5C4848` sampled'dan) | balon metni, ikincil gövde |
| `--ink-500` | `#66585C` | approximated | caption, yardımcı metin (min. AA) |
| `--ink-300` | `#B8ADB2` | approximated | pasif ikon, placeholder **değil** (placeholder `--ink-500`) |
| `--ink-200` | `#DED5D3` | approximated | input kenarlığı |
| `--ink-100` | `#EFEFED` | sampled (çubuk izi) | `--track`, skeleton zemini |
| `--ink-50` | `#F6F6F4` | sampled (ayraç) | `--border-subtle`, tablo zebra |

### 3.3 Yüzeyler ve gradient'ler

| Token | Değer |
|---|---|
| `--surface-page` | `#EFEAE4` (sampled; gradient desteklenmezse düz zemin) |
| `--gradient-page` | `linear-gradient(180deg, #EFDBD2 0%, #F0E3DB 45%, #EFEAE4 100%)` (sampled duraklar) |
| `--surface-card` | `#FCFCF9` (sampled) |
| `--surface-raised` | `#FFFFFF` (dialog, popover, telefon önizleme ekranı) |
| `--surface-bubble-user` | `var(--orange-100)` |
| `--surface-bubble-ai` | `var(--surface-card)` + `--border-subtle` |
| `--surface-sunken` | `#F4EFEB` (approximated; kod/chip alanı, editör zemini) |
| `--track` | `#EFEFED` (sampled) |
| `--border-subtle` | `#F0EBE6` (approximated, kart içi ayraç) |
| `--border-default` | `#E4DAD5` (approximated, kart dış çizgisi, input) |
| `--gradient-brand-vivid` | `linear-gradient(180deg, #FA5B23 0%, #EA2D2B 100%)` (sampled) — **yalnızca dekor**: çubuk dolgusu, ikon zemini; üstüne metin yazılmaz |
| `--gradient-cta` | `linear-gradient(180deg, #D63A1E 0%, #C42A26 100%)` — beyaz metinle AA (4.68–5.67) |
| `--gradient-cta-hover` | `linear-gradient(180deg, #C8361B 0%, #B02322 100%)` |

### 3.4 Semantik ve durum renkleri

Hata rengi marka turuncusundan bilinçli olarak **kırmızı-mor (crimson)** yöne ayrıldı; yine de hata hiçbir zaman yalnızca renkle anlatılmaz (ikon + metin).

| Token | Metin/ikon (light) | Zemin (light) | Dark metin | Kullanım |
|---|---|---|---|---|
| `--success` | `#17673F` | `#E3F2EA` | `#4FC08D` | `validated`, `sent` |
| `--warning` | `#855000` | `#FBEFD9` | `#F2B34A` | near-limit, 409 uyarısı |
| `--error` | `#B4233C` | `#FBE4E8` | `#FF6B81` | `invalid`, `failed`, over-limit, 4xx/5xx |
| `--error-strong` | `#9C1F35` | — | — | hata başlığı, hover |
| `--info` | `#2557A8` | `#E4ECF8` | `#6FA8FF` | `sending`, bilgi notları |
| `--neutral-status` | `#66585C` | `#EFEAE7` | `#9C9097` | `queued`, `draft` |

Teslimat ve akış durum eşlemesi (renk + ikon + Türkçe etiket, ikon `@lucide/vue`):

| Durum | Ton | İkon | Etiket (i18n varsayılanı) |
|---|---|---|---|
| içerik `draft` (akıyor) | neutral | `PenLine` + caret | Taslak |
| içerik `validated` | success | `CircleCheck` | Doğrulandı |
| içerik `invalid` | error | `CircleAlert` | Geçersiz |
| akış kesildi (son olay yok) | warning | `Unplug` | Akış kesildi |
| teslimat `queued` | neutral | `Clock` | Kuyrukta |
| teslimat `sending` | info | `Loader` (dönmez, reduced-motion'da sabit) | Gönderiliyor |
| teslimat `sent` | success | `Check` | Gönderildi |
| teslimat `failed` | error | `X` | Başarısız |
| kampanya `completed` | brand (`--orange-700` metin, `--orange-50` zemin) | `Flag` | Tamamlandı |

### 3.5 Kanal renkleri

Turuncudan ve semantik renklerden ayrışan iki ton; çubuk, sekme göstergesi ve kanal rozetinde kullanılır. Metin varyantları AA'yı geçer.

| Token | Grafik | Metin | Zemin | Dark grafik/metin |
|---|---|---|---|---|
| `--channel-sms` | `#0E7C7E` (teal) | `#0B6668` | `#E1F1F1` | `#3CC3C5` |
| `--channel-email` | `#3B5BDB` (indigo-mavi) | `#3049B8` | `#E6EAFB` | `#8C9BFF` |

### 3.6 Dark tema (opsiyonel, paralel)

`--surface-page #17121A`, `--gradient-page linear-gradient(180deg,#221418 0%,#17121A 60%)`, `--surface-card #221B24`, `--surface-raised #2A222C`, `--surface-sunken #1C161E`, `--track #3A3036`, `--border-default #3A3036`, `--ink-900 #F4EEF0`, `--ink-700 #E2D9DC`, `--ink-500 #B8ADB2`, `--orange-700`(metin rolü) `#FF7A5C`, `--orange-600`(büyük metin rolü) `#F66850`, `--orange-500 #F4462A`. Gradient'ler aynı kalır (beyaz metin oranı zeminden bağımsız). Dark değerler approximated.

### 3.7 Kontrast tablosu (WCAG 2.x, hesaplandı)

AA eşikleri: normal metin 4.5, büyük metin (≥24 px veya ≥18.66 px bold) ve grafik/UI 3.0.

| Ön plan | `#EFDBD2` sayfa üst | `#EFEAE4` sayfa alt | `#FCFCF9` kart | `#FFFFFF` | `#F4DDD7` balon | `#17121A` dark | `#221B24` dark kart |
|---|---|---|---|---|---|---|---|
| `--orange-500 #F4462A` | 2.73 ✗ | 3.05 büyük | 3.55 büyük | 3.65 büyük | 2.81 ✗ | 5.06 AA | 4.60 AA |
| ref. başlık `#EC3430` | 3.08 büyük | 3.44 büyük | 4.01 büyük | 4.12 büyük | 3.17 büyük | 4.48 büyük | 4.08 büyük |
| `--orange-600 #D63A1E` | 3.51 büyük | 3.92 büyük | **4.56 AA** | 4.68 AA | 3.61 büyük | 3.94 | 3.59 |
| `--orange-700 #B83015` | **4.53 AA** | 5.06 AA | 5.89 AA | 6.05 AA | 4.66 AA | 3.05 | 2.78 |
| dark metin `#FF7A5C` | — | — | — | — | — | 7.20 AA | 6.55 AA |
| `--ink-900 #241C20` | 12.47 | 13.91 | 16.19 | 16.64 | 12.81 | — | — |
| `--ink-700 #3C2C2C` | 9.90 | 11.05 | 12.86 | 13.22 | 10.18 | — | — |
| `--ink-500 #66585C` | 5.05 | 5.64 | 6.56 | 6.74 | 5.19 | — | — |
| ref. gri `#B0B0B0` | 1.62 ✗ | 1.81 ✗ | 2.11 ✗ | 2.17 ✗ | — | — | — |
| `--success #17673F` | 5.16 | 5.76 | 6.70 | 6.89 | — | — | — |
| `--warning #855000` | 5.00 | 5.58 | 6.50 | 6.68 | — | — | — |
| `--error #B4233C` | 4.85 | 5.41 | 6.29 | 6.47 | 4.98 | — | — |
| `--info #2557A8` | 5.23 | 5.84 | 6.79 | 6.98 | — | — | — |
| `--channel-sms` metin `#0B6668` | 5.05 | 5.64 | 6.56 | 6.74 | — | — | — |
| `--channel-email` metin `#3049B8` | 5.68 | 6.34 | 7.37 | 7.58 | — | — | — |

Buton üstü beyaz metin: referans gradient üstü `#FA5B23` **3.19 ✗**, alt `#EA2D2B` 4.28 ✗ → bu yüzden `--gradient-cta` (`#D63A1E` 4.68 → `#C42A26` 5.67) tanımlandı. Grafik: `--orange-500` çubuk, `--track #EFEFED` iz üstünde 3.17 (UI ≥3 geçer).

## 4. Metin rengi politikası ve tipografi

### 4.1 Metin rengi politikası (sahip kararı)

**Turuncu, metin vurgu rengidir.** Referanslardaki gibi "ikinci satır / anahtar ifade turuncu" dili uygulamaya şöyle çevrilir:

| Metin rolü | Renk | Not |
|---|---|---|
| Ekran başlığının vurgu kısmı (display, h1'de anahtar ifade) | `--text-accent-display` | Örn. "Kampanyanı **tek cümleyle** anlat" |
| h2–h4 başlıklar | `--text-accent-heading` | Kart başlıkları dahil |
| Vurgulu kelime (`<strong>` rolündeki) | `--text-accent` | Gövde içinde, cümle başına en fazla bir |
| Anahtar sayılar: kitle sayısı, karakter sayacı (normal), teslimat sayaçları | `--text-accent-numeral` | Durum renkli sayaçlar istisna: başarısız sayısı `--error` |
| Link, aktif stepper etiketi, aktif sekme etiketi | `--text-accent` | Altı çizili link; hover `--orange-800` |
| Uzun gövde metni, input/textarea içeriği, LLM tarafından üretilen metin, tablo hücreleri, caption, placeholder | `--ink-*` | **Turuncu yapılmaz** (okunabilirlik + LLM metni "veri"dir) |
| Buton etiketi | beyaz (primary), `--orange-700` (secondary/ghost) | |

**Boy-zemin kuralı:** Küçük metin (≤ 18 px regular, ≤ 18.66 px bold altı) her açık zeminde `--orange-700`. Büyük metin `--orange-600`; yalnızca `--surface-card`/beyaz üstünde 24 px+ display için `--orange-500` serbesttir. Sayfa gradient'i üstünde (`#EFDBD2`) `--orange-500` metin **yasak** (2.73).

**Tek anahtar (switchable token set):** Bileşenler turuncu metne doğrudan `--orange-*` ile değil, yalnızca aşağıdaki rol token'larıyla erişir. Sahip daha az/çok turuncu isterse yalnızca bu blok değişir:

```css
:root {                                   /* preset: "accent" (varsayılan) */
  --text-accent:          var(--orange-700);
  --text-accent-heading:  var(--orange-700);
  --text-accent-display:  var(--orange-600);
  --text-accent-numeral:  var(--orange-700);
}
:root[data-accent-text="minimal"] {       /* yalnızca display + sayı turuncu */
  --text-accent-heading:  var(--ink-900);
  --text-accent:          var(--ink-900);
}
:root[data-accent-text="bold"] {          /* gövde vurguları da turuncu, kart başlıkları büyük boy */
  --text-accent-heading:  var(--orange-600);
}
```

Varsayılan preset `accent` (VARSAYIM). `minimal` linkleri altı çizili mürekkep olarak bırakır; erişilebilirlik bozulmaz.

### 4.2 Font aileleri

| Rol | Aile | Yükleme | Ağırlıklar | Fallback |
|---|---|---|---|---|
| Sans (her şey) | **Figtree** (approximated eşleşme) | `@fontsource-variable/figtree` 5.3.0 (self-host, değişken) | 400, 500, 600, 700 | `"Figtree Variable", "Segoe UI", "Helvetica Neue", Arial, sans-serif` |
| Mono (değişken chip, sayaç, kod) | **JetBrains Mono** | `@fontsource-variable/jetbrains-mono` 5.3.0 | 400, 500 | `ui-monospace, "SF Mono", Menlo, Consolas, monospace` |

Self-host gerekçesi: `docker compose up` çevrimdışı/izole ağda da çalışmalı; Google CDN'e istek atılmaz, gizlilik ve CSP sade kalır. `font-display: swap` (Fontsource varsayılanı). Türkçe karakter (`ş ğ ı İ ç ö ü`) Figtree'nin latin-ext alt kümesindedir — **DOĞRULANMADI**, kurulumda `İstanbul` ile görsel test yapılır.

### 4.3 Tip ölçeği

Rakamlar her sayaç/tabloda `font-variant-numeric: tabular-nums` kullanır (akarken genişlik oynamasın).

| Token | Boyut / satır | Ağırlık | Tracking | Kullanım |
|---|---|---|---|---|
| `display` | 44 / 52 px (mobil 32 / 40) | 500 | -0.02em | Brief ekranı karşılama başlığı |
| `h1` | 32 / 40 (mobil 26 / 34) | 600 | -0.015em | Ekran başlığı |
| `h2` | 24 / 32 | 600 | -0.01em | Bölüm, kart grubu |
| `h3` | 20 / 28 | 600 | -0.005em | Kart başlığı |
| `h4` | 16 / 24 | 600 | 0 | Alt başlık, form grubu |
| `body-lg` | 18 / 28 | 400 | 0 | Brief textarea, SMS önizleme |
| `body` | 16 / 24 | 400 | 0 | Varsayılan gövde |
| `body-sm` | 14 / 20 | 400 | 0 | Tablo, feed |
| `caption` | 12 / 16 | 500 | 0.01em | Rozet, yardımcı metin, eksen etiketi |
| `overline` | 12 / 16 | 600 | 0.06em, uppercase | Stepper adım numarası ("ADIM 2") — Türkçe büyük harf için `lang="tr"` zorunlu (`i → İ`) |
| `mono` | 14 / 20 | 500 | 0 | `{{first_name}}` chip, kod |
| `numeral-xl` | 40 / 44 | 600 | -0.02em | Kitle toplamı, dashboard ana sayaç |
| `numeral-md` | 24 / 28 | 600 | -0.01em | Kanal sayıları |
| `numeral-sm` | 13 / 16 | 600 (mono) | 0 | Karakter sayacı `142/160` |

## 5. Biçim dili

**Boşluk (4 px tabanı):** `--space-0 0`, `-1 4px`, `-2 8px`, `-3 12px`, `-4 16px`, `-5 20px`, `-6 24px`, `-8 32px`, `-10 40px`, `-12 48px`, `-16 64px`. Kart iç boşluğu 24 (mobil 16); kartlar arası 16; bölümler arası 40. Referanslardaki çubuk satır ritmi (~30 px) → `ProgressRow` satır yüksekliği 32.

**Köşe yarıçapı:** `--radius-pill 9999px` (buton, chip, rozet, çubuk, sekme göstergesi) · `--radius-card 16px` · `--radius-card-lg 20px` (dashboard kanal kartı, telefon önizleme ekranı) · `--radius-bubble 12px` (balon; kullanıcı balonunun sağ alt köşesi 4px) · `--radius-input 12px` · `--radius-sm 8px` (tooltip, kod bloğu). shadcn-vue'nun `--radius` taban değeri `1rem` yapılır (§6).

**Gölge (sıcak mürekkep tabanlı):**
- `--shadow-card`: `0 1px 2px rgb(36 28 32 / 0.04), 0 8px 24px rgb(36 28 32 / 0.06)` (görsel 1'deki çok yumuşak gölge, approximated)
- `--shadow-raised`: `0 2px 4px rgb(36 28 32 / 0.06), 0 16px 40px rgb(36 28 32 / 0.12)` (görsel 2 kartı, dialog)
- `--shadow-cta`: `0 6px 16px rgb(196 42 38 / 0.28)`; hover `0 8px 20px rgb(196 42 38 / 0.34)`
- `--shadow-focus`: `0 0 0 2px var(--surface-card), 0 0 0 4px var(--orange-600)`

**Kenarlık:** 1 px `--border-default` kart dışı; 1 px `--border-subtle` kart içi ayraç; input focus'ta kenarlık `--orange-600` + `--shadow-focus`; hata durumunda 1.5 px `--error`.

**Gradient kuralları:** `--gradient-cta` yalnızca birincil butonda. `--gradient-brand-vivid` yalnızca dekorda (çubuk dolgusu, Brief ekranındaki küçük ikon zemini). Sayfa zemini `--gradient-page` `background-attachment: fixed` ile. Mor/mavi gradient yok.

**Hareket:**

| Token | Değer | Kullanım |
|---|---|---|
| `--dur-fast` | 120 ms | hover, focus, chip |
| `--dur-base` | 200 ms | sekme, rozet geçişi, toast giriş |
| `--dur-slow` | 320 ms | adım geçişi, dialog |
| `--dur-bar` | 600 ms | çubuk genişliği (SSE güncellemesi arası yumuşatma) |
| `--ease-standard` | `cubic-bezier(0.2, 0, 0, 1)` | genel |
| `--ease-emphasized` | `cubic-bezier(0.3, 0, 0, 1)` | adım geçişi, sayaç vurgusu |

- **Akış caret'i:** metnin sonunda 2 px × 1.1em `--orange-500` dikey çubuk; 1 s `steps(2)` yanıp söner; son olay (`validated`/`invalid`) gelince 200 ms'de söner ve yerine durum rozeti gelir. Caret `aria-hidden="true"`.
- **Rozet geçişi:** `draft → validated` arası 200 ms fade + 4 px yukarı kayma; `invalid` için ek olarak alan kenarlığı bir kez 2 px yatay "nudge" (yalnızca hareket izinliyse).
- **Sayaç tween'i:** dashboard sayaçları 300 ms'de eski değerden yeniye sayar; büyük değişimde 1 kez `--orange-50` zemin flaşı.
- **`prefers-reduced-motion: reduce`:** caret yanıp sönmez (sabit), tween ve nudge kapalı, çubuk geçişi 0 ms, adım geçişi yalnızca opaklık 120 ms; `Loader` ikonu dönmez. Global: `@media (prefers-reduced-motion: reduce) { *, *::before, *::after { animation-duration: 0.01ms !important; animation-iteration-count: 1 !important; transition-duration: 0.01ms !important; } }` + bileşen bazlı istisnalar.

## 6. Component library kararı ve kurulum

### 6.1 Karar: **shadcn-vue + Reka UI + Tailwind CSS v4**

Sürümler npm registry'den 2026-10-26'da okundu (`/latest`); kurulum adımları shadcn-vue resmi sitesinden (Vite kurulumu, Theming, Sonner sayfaları) doğrulandı.

| Ölçüt | shadcn-vue + Reka UI | PrimeVue 5 | Naive UI | Vuetify 4 | Element Plus |
|---|---|---|---|---|---|
| Erişilebilirlik | Reka UI (Radix portu) WAI-ARIA desenleri; Stepper klavye desteği doğrulandı | iyi | orta | iyi | orta |
| Bu palete tema | CSS değişkeni + `@theme inline`; token'larımız birebir eşlenir | design token preset (`@primeuix/themes`) — güçlü ama kendi token dili | JS theme overrides (CSS-in-JS) | Material'e bağlı görünüm | admin görünümü, SCSS değişkenleri |
| Modern görünüm / referansa uyum | boş tuval; pill + gradient + yumuşak gölge kolay | Aura preset'i nötr; özelleştirme gerekli | iyi | Material kimliği baskın | kurumsal-panel hissi |
| Akış metni + canlı dashboard | Bileşen kaynağı repoda; caret/aria-live davranışı doğrudan kodumuzda | kapalı bileşen davranışını ezmek gerekir | aynı | aynı | aynı |
| Test kolaylığı (`data-testid`) | kaynak bizde → `data-testid` attr fallthrough'u ve iç öğelere aktarım tam kontrolde | iç öğelere testid için `pt` (passthrough) API'si | kısıtlı | kısıtlı | kısıtlı |
| Bundle | yalnızca eklenen bileşenler + Reka primitive'leri | tree-shake edilir, tema runtime'ı var | CSS-in-JS runtime | büyük | büyük |

**Gerekçe:** Bu ürünün ayırt edici yüzeyleri (akan metin bloğu, karakter sayacı, telefon/email önizleme, teslimat çubukları) zaten custom; bize gereken erişilebilir primitive'ler (Dialog, AlertDialog, Tabs, Stepper, Tooltip, Toast) ve palete tam uyum. shadcn-vue bileşenleri repoya kopyaladığı için `data-testid`, `aria-*` ve token'lar üzerinde tam kontrol verir; Tailwind v4'ün CSS-first `@theme` yaklaşımı token tablomuzla bire bir örtüşür. PrimeVue 5 güçlü ikinci adaydır (zengin DataTable), fakat bu projede tablo ihtiyacı küçük. Rakip sütunlarındaki nitel değerlendirmeler bellekten yazıldı (**DOĞRULANMADI**); yalnızca sürümleri registry'den teyit edildi (PrimeVue 5.0.4, `@primeuix/themes` 3.1.0, Naive UI 2.45.3, Vuetify 4.3.0, Element Plus 2.14.7). PrimeVue tema dokümanı `primevue.dev`'e taşınmış, okunmadı.

### 6.2 Paketler (registry'de doğrulanan son sürümler)

| Paket | Sürüm | Not |
|---|---|---|
| `vue` | 3.5.44 | |
| `pinia` | 4.1.0 | **Major sürüm 4** — API farkı DOĞRULANMADI, kurulumda changelog okunur |
| `tailwindcss`, `@tailwindcss/vite` | 4.3.3 | Vite eklentisi; `src/style.css` içinde `@import "tailwindcss";` (doğrulandı) |
| `shadcn-vue` (CLI) | 2.8.2 | `pnpm dlx shadcn-vue@latest init`, `… add button` (doğrulandı); bağımlılığı `reka-ui` |
| `reka-ui` | 2.11.0 | |
| `tw-animate-css` | 1.4.0 | shadcn-vue v4 şablonunun animasyon katmanı (kullanımı DOĞRULANMADI) |
| `class-variance-authority` | 0.7.1 | buton varyantları |
| `vue-sonner` | 2.0.9 | shadcn-vue Sonner bileşeni bunu kullanır; `import 'vue-sonner/style.css'` gerekir (doğrulandı) |
| `@lucide/vue` | 1.61.0 | **`lucide-vue-next` deprecated** ("Please use @lucide/vue instead", registry) |
| `@fontsource-variable/figtree`, `@fontsource-variable/jetbrains-mono` | 5.3.0 | |
| `@vueuse/core` | 15.0.0 | `useEventSource` vb. yardımcılar (opsiyonel; API DOĞRULANMADI) |
| `vitest` / `@vue/test-utils` | 5.0.5 / 2.5.1 | |

Kurulum sırası (doğrulanan Vite rehberi): Vite `vue-ts` şablonu → `tailwindcss @tailwindcss/vite` → `tsconfig.json` ve `tsconfig.app.json`'a `@/*` alias → `vite.config.ts`'e `vue(), tailwindcss()` ve `@` alias (`@types/node`) → `shadcn-vue init` (base color: Neutral, sonra token'larla ezilir) → ihtiyaç duyulan bileşenler `add`. Kullanılacak shadcn-vue bileşenleri (sidebar'da varlığı doğrulandı): Button, Badge, Card, Tabs, Stepper, Progress, Textarea, Input, Dialog, Alert Dialog, Alert, Skeleton, Sonner, Tooltip, Separator, Scroll Area, Empty, Spinner. Listede `Bubble`, `Message`, `Message Scroller` da görünüyor; içerikleri okunmadı (**DOĞRULANMADI**) — brief composer için değerlendirilebilir.

Grafik kütüphanesi **yok**: referanslardaki yatay çubuklar `ProgressBar` (custom, `div` + `role="progressbar"` veya segmentli çubukta `role="img"` + metin özeti) ile çizilir. shadcn-vue `Chart` bileşeni gereksiz ağırlık getirir.

### 6.3 Token → CSS değişkeni → Tailwind eşlemesi

Üç katman: (1) **ham palet** (`--orange-500`, `--ink-700` …), (2) **rol token'ları** (`--text-accent`, `--surface-card`, shadcn adları), (3) **Tailwind utility'leri** `@theme inline` ile. Bileşenler yalnızca 2. ve 3. katmanı kullanır; hex değer bileşen içinde yasak (lint: `stylelint` `color-no-hex` bileşen dosyalarında — ÖNERİ).

```css
/* src/styles/tokens.css — §3 tablolarındaki tüm ham değerler burada */
:root {
  --background: var(--surface-page);       --foreground: var(--ink-700);
  --card: var(--surface-card);             --card-foreground: var(--ink-700);
  --popover: var(--surface-raised);        --popover-foreground: var(--ink-900);
  --primary: var(--orange-600);            --primary-foreground: #FFFFFF;
  --secondary: var(--orange-50);           --secondary-foreground: var(--orange-700);
  --muted: var(--surface-sunken);          --muted-foreground: var(--ink-500);
  --accent: var(--orange-50);              --accent-foreground: var(--orange-700);
  --destructive: var(--error);             --border: var(--border-default);
  --input: var(--ink-200);                 --ring: var(--orange-600);
  --radius: 1rem;
}
@theme inline {
  --color-background: var(--background);   --color-foreground: var(--foreground);
  --color-primary: var(--primary);         --color-primary-foreground: var(--primary-foreground);
  --color-accent-text: var(--text-accent); --color-success: var(--success);
  --color-warning: var(--warning);         --color-error: var(--error);  --color-info: var(--info);
  --color-sms: var(--channel-sms);         --color-email: var(--channel-email);
  --font-sans: "Figtree Variable", "Segoe UI", "Helvetica Neue", Arial, sans-serif;
  --font-mono: "JetBrains Mono Variable", ui-monospace, Menlo, monospace;
  /* … tüm rol token'ları aynı desenle */
}
```

shadcn-vue varsayılanı `oklch` kullanır (doğrulandı); hex değerlerimiz doğrudan geçerlidir, dönüştürme zorunlu değil. Dark tema `.dark` sınıfıyla (shadcn-vue'nun `@custom-variant dark (&:is(.dark *))` deseni, doğrulandı); ilk sürümde yalnızca light.

## 7. Component envanteri

"Base" sütunu: shadcn-vue bileşeni (Reka UI üzerine) veya custom. Tüm bileşenler `data-testid`'yi sözleşmeden alır; burada tanımlanmaz.

| Bileşen (çalışma adı) | Amaç | Base | Varyantlar | Durumlar | REQ |
|---|---|---|---|---|---|
| `Button` | Eylemler | shadcn `Button` + cva | `primary` (pill, `--gradient-cta`, beyaz 600 metin, `--shadow-cta`, sağda isteğe bağlı `ArrowUpRight` ↗ — referans dili) · `secondary` (pill, `--orange-50` zemin, `--orange-700` metin, 1 px `--orange-200`) · `ghost` (zeminsiz, `--text-accent`) · `danger-ghost` (`--error`). Boy: `sm` 32 / `md` 40 / `lg` 48 px; yatay 16/20/24 | default, hover (gradient-hover, gölge büyür), active (scale .98), focus-visible (`--shadow-focus`), disabled (opaklık .45, gölge yok, `cursor:not-allowed`, `aria-disabled`), loading (etiket kalır, solda `Spinner`, tıklama kilitli) | — |
| `AppHeader` | Wordmark, sağlayıcı rozeti, kampanya durumu | custom | desktop / mobil (wordmark kısalır: "SCC") | — | REQ-AI-03 |
| `ProviderBadge` | "Mock LLM" / "Claude" | shadcn `Badge` | `mock`: `--warning` zemin+metin, `FlaskConical` ikon, metin "Mock LLM" · `claude`: nötr, `Sparkles` ikon | yükleniyor (skeleton pill 88×24), bilinmiyor (gizli değil: "Sağlayıcı ?" nötr) | REQ-AI-03 |
| `CampaignStepper` | Brief → Kitle → İçerik → Onay → Takip | shadcn `Stepper` (Reka) | yatay (≥768) / kompakt ("Adım 3/5 · İçerik" + 5 segmentli mini çubuk) | adım: `completed` (`--orange-600` dolu daire + `Check`), `active` (2 px `--orange-600` halka, etiket `--text-accent` 600), `upcoming` (`--ink-300`), `locked` (kampanya durumu izin vermiyor; tıklanamaz, tooltip nedenini söyler) | durum makinesi |
| `BriefComposer` | Sohbet benzeri brief girişi | shadcn `Textarea` + custom kabuk | boş, örnek brief chip'iyle ("Örneği kullan"), gönderilmiş (kullanıcı balonu olarak üstte kalır) | idle, focus, typing (sayaç `0/2000`), near-limit (≥1800 → `--warning`), over-limit (>2000 → `--error`, gönder pasif), submitting (gönder butonu loading, textarea `readonly`), error (422/413 inline, 502 kart). ⌘/Ctrl+Enter gönderir; Enter yeni satır | REQ-AUD-00, REQ-AUD-05 |
| `ChatBubble` | Brief ve sistem yanıtları | custom (shadcn `Bubble` değerlendirilir) | `user` (sağ, `--surface-bubble-user`, `--ink-600` metin) · `assistant` (sol, kart zemini, solda 20 px marka glyph'i — Claude logosu **değil**, üç kısa yatay çubuk) | normal, pending (3 nokta skeleton) | REQ-AUD-00 |
| `AudienceSummaryCard` | Toplam eşleşen + kanal bazlı sayılar + izin notu | shadcn `Card` + custom | yükleniyor, hazır, boş (0 kişi), hata | toplam `numeral-xl` `--text-accent-numeral`; altında iki satır `ProgressRow` (SMS / Email: sayı ve toplam içindeki oran çubuğu kanal renginde); not satırı `Info` ikon + "Sayılar kanal izni ve iletişim bilgisi olanlara göre hesaplandı" (`--ink-500`). 0 kişi: uyarı tonu, "Bu kural kimseyi seçmedi" + "Brief'i düzenle" | REQ-AUD-03, REQ-AUD-04 |
| `SegmentRuleView` | Kuralın insan-okur özeti (ör. "Şehir = İstanbul · Son 30 gün sepete ekledi · Satın almadı") | custom | chip listesi / açılır JSON (mono, salt okunur, `textContent`) | — | REQ-AUD-00/01 |
| `ChannelTabs` | SMS / Email geçişi | shadcn `Tabs` | her sekmede kanal noktası (renk) + durum rozeti mini (validated/invalid) | aktif: alt 2 px pill gösterge kanal renginde, etiket `--text-accent`; pasif; hata içeren sekme etiketinde `CircleAlert` | REQ-CNT-01..04 |
| `StreamingText` | Akan içerik bloğu | custom | alan başına (SMS gövdesi, email konusu, email gövdesi) | `idle` (skeleton 3 satır), `streaming` (metin + caret, rozet "Taslak"), `validated` (caret yok, rozet "Doğrulandı"), `invalid` (rozet "Geçersiz" + alan altında hata listesi), `retrying` (sunucu son olmayan `invalid` gönderdi: taslak temizlenir, üstte info satırı "Model çıktısı düzeltiliyor…"), `interrupted` (son olay yok: taslak soluk, uyarı "Akış yarıda kesildi" + "Yeniden üret") | REQ-CNT-04, REQ-AI-01, REQ-UI-01 |
| `StatusBadge` | Durum rozetleri | shadcn `Badge` | §3.4 tablosundaki tüm durumlar; `sm` (20 px) / `md` (24 px); pill, ikon + metin, zemin = semantik zemin | — | REQ-CNT-04, REQ-TRK-01 |
| `VariableChip` | `{{first_name}}` vb. | custom | `insert` (editör araç çubuğunda, tıklayınca imleç konumuna ekler) · `inline` (önizlemede renderlanmamış hali) · `invalid` (izinsiz değişken: `--error` kenarlık + üstü çizili değil, `CircleAlert`) | default, hover, focus, disabled (sending/completed) | REQ-CNT-03 |
| `CharCounter` | 160/80 sayacı | custom | `sms` (160), `subject` (80) | `normal` (`--ink-500` "142/160"), `near-limit` (≥ %90: 144/160, 72/80 → `--warning` + `TriangleAlert`), `over-limit` (> limit → `--error`, "−3" fazlalık, onay pasif). Sayım Unicode code point (`[...s].length`); ikinci satır "Kişiselleştirme sonrası en fazla: N" yalnızca API bu değeri dönerse | REQ-CNT-01/02/05 |
| `ContentEditor` | İçeriği düzenleme | shadcn `Textarea` / `Input` | SMS gövdesi (textarea, `body-lg`), email konusu (input), email gövdesi (textarea, `body`) | readonly (akış sürerken), editable, dirty (kaydedilmemiş; "Kaydet ve doğrula" aktif), validating (spinner), invalid (alan bazlı hata), locked (`sending`/`completed`: kilit ikonu + açıklama) | REQ-APR-01/02/03 |
| `SmsPreview` | Telefon balonu önizlemesi | custom | örnek alıcı adıyla render, ham şablon (chip'li) | boş, render, uzun mesaj (balon büyür, kaydırma yok) | REQ-APR-01, REQ-UI-01 |
| `EmailPreview` | Sandbox'lı email çerçevesi | custom | konu satırı + gövde iframe | yükleniyor, render, hata | REQ-APR-01, REQ-UI-01 |
| `ApprovalBar` | Onay eylemi | custom (sticky) | desktop (kart altı) / mobil (ekran altına yapışık) | `disabled` (her iki kanal `validated` değilse; neden metni görünür: "SMS doğrulanmadı" vb.), `ready`, `submitting`, `approved` (yeşil `CircleCheck` + "Onaylandı · Gönder" birincil butonu), `stale` (onay sonrası düzenleme: "Onay düştü, yeniden onayla") | REQ-APR-01/03, REQ-CNT-04/05 |
| `ConfirmDialog` | Onay/gönder/yeniden üret teyidi | shadcn `Alert Dialog` | `approve`, `send` (kanal sayıları özetlenir: "SMS 1.204 · Email 987 kişiye gidecek"), `regenerate` (mevcut içerik silinecek) | açık, submitting, hata (diyalog içinde inline) | REQ-APR-01, REQ-SND-01 |
| `ProgressBar` / `ProgressRow` | Referans çubuk dili | custom (`role="progressbar"`) | `single` (tek dolgu `--gradient-brand-vivid`), `segmented` (sent `--success` · failed `--error` · sending `--info` · queued `--track` desen); satır: solda etiket, ortada çubuk (8 px, pill), sağda sayı + yüzde (`numeral-sm`) | indeterminate yok (her zaman sayı); güncelleme `--dur-bar` | REQ-AUD-03, REQ-TRK-01 |
| `DeliveryCounters` | Kuyrukta / Gönderiliyor / Gönderildi / Başarısız | custom (Card) | kanal başına ve toplam | live, stale (SSE koptu: sayılar soluk + "Son güncelleme 12 sn önce"), completed | REQ-TRK-01 |
| `ActivityFeed` | Canlı olay akışı (son 50) | custom + shadcn `Scroll Area` | — | boş ("Henüz olay yok"), live, paused (kullanıcı yukarı kaydırdıysa "Yeni 12 olay ↓"), kopuk. Satır: zaman (mono) · kanal noktası · `StatusBadge` · kısa metin; kişisel veri (telefon/e-posta) gösterilmez, yalnızca müşteri kimliği | REQ-TRK-01, REQ-SEC-01 (ruhu) |
| `Toast` | Geçici geri bildirim | shadcn `Sonner` (`vue-sonner`) | success, info, warning, error | otomatik kapanma 5 s (error: kalıcı, kapat butonu) | HTTP kodları |
| `InlineError` / `ErrorCard` | Alan ve bölüm hataları | shadcn `Alert` | field (alan altı, 14 px, `--error`, ikon), section (kart, başlık + açıklama + eylem) | — | HTTP kodları |
| `EmptyState` | Boş durumlar | shadcn `Empty` | ikon (48 px, `--orange-300` çizgi), başlık, açıklama, tek eylem | — | — |
| `Skeleton` | Yükleniyor | shadcn `Skeleton` | satır, sayı bloğu, çubuk, balon | `--track` zemin, 1.2 s shimmer (reduced-motion'da düz) | — |

**HTTP kodu → arayüz eşlemesi** (hata gövdesi biçimi sözleşmede; mesajlar i18n'den):

| Kod | Gösterim | Eylem |
|---|---|---|
| 404 | Tam sayfa `EmptyState` "Kampanya bulunamadı" | "Yeni kampanya" |
| 409 | `Toast` warning "Kampanya durumu değişti" + durum yeniden çekilir, stepper güncellenir; onay bağlamında `ApprovalBar` `stale` | "Yenile" |
| 413 | Brief/editör altında field hata "Metin çok büyük" | — |
| 422 | Alan bazlı `InlineError` (hata listesi alan anahtarına göre dağıtılır); eşlenemeyen hata bölüm `ErrorCard`'ında | odak ilk hatalı alana |
| 502 | Bölüm `ErrorCard` "Model geçerli bir çıktı üretemedi" (segment); içerikte son `invalid` olayı `StreamingText` `invalid` | "Yeniden üret" |
| Ağ / SSE kopması | Üst şerit (sticky, `--warning`) "Bağlantı koptu, yeniden bağlanılıyor…" | otomatik yeniden bağlanma; içerik akışında `interrupted` |

## 8. Ekranlar ve akış

Kabuk: `AppHeader` (64 px; solda wordmark Figtree 700 `--ink-900` + "Composer" kelimesi `--text-accent-display`; sağda `ProviderBadge` ve kampanya `StatusBadge`) → `CampaignStepper` → içerik (maks. 1120 px, ortalı). Breakpoint'ler (Tailwind varsayılanı): `sm 640`, `md 768`, `lg 1024`, `xl 1280`. Stepper adımları yalnızca kampanya durumunun izin verdiği yere kadar tıklanabilir; ileri gitmek her zaman o adımın birincil eylemiyle olur.

Adım erişimi **`docs/contracts/ui.md` §1'deki tabloya göre** belirlenir (bu dosyayla çelişirse o geçerlidir). Özet:

| Adım | Erişilebilir olduğu kampanya durumları | Ana eylem |
|---|---|---|
| 1 Brief | her zaman (`sending`/`completed`'de salt okunur) | "Kitleyi çıkar" |
| 2 Kitle | `audience_ready` ve sonrası | "İçeriği üret" |
| 3 İçerik | `audience_ready` ve sonrası (içerik burada üretilir) | "Önizle ve onayla" |
| 4 Onay | `content_ready`, `approved` (ve sonrası salt okunur) | "Onayla" → "Gönder" |
| 5 Takip | `sending`, `completed` | — (salt okunur) |

### 8.1 Brief (desktop)

```
┌ Smart Campaign Composer ─────────────────────── [⚗ Mock LLM] [Taslak] ┐
│  ● Brief ── ○ Kitle ── ○ İçerik ── ○ Onay ── ○ Takip                 │
│                                                                      │
│          Kampanyanı                                                  │
│          tek cümleyle anlat            ← 2. satır --text-accent-display│
│          Kitleyi ve mesajları Claude önerir, son kararı sen verirsin. │
│  ┌──────────────────────────────────────────────────────────────┐    │
│  │ Son 30 günde sepetine ürün ekleyip…                          │    │
│  │                                                              │    │
│  │ [Örnek brief'i kullan]                    0/2000  ( Kitleyi çıkar ↗ )│
│  └──────────────────────────────────────────────────────────────┘    │
│  ⓘ Gerçek kimseye mesaj gitmez; alıcılar sahte seed verisidir.       │
└──────────────────────────────────────────────────────────────────────┘
```
Durumlar: boş, yazıyor, gönderiliyor (balon + 3 nokta), 422/413 field hatası, 502 `ErrorCard`.

### 8.2 Kitle

```
│  ┌ (sağa yaslı balon) Son 30 günde sepetine… ┐                       │
│  ┌ Kitle ───────────────────────────────────────────────────────┐    │
│  │ ≡ Şehir = İstanbul · Sepete ekledi ≤30 gün · Satın almadı [JSON]│  │
│  │                                                              │    │
│  │ 1.482  kişi kurala uyuyor        ← numeral-xl, turuncu        │    │
│  │ SMS    ████████████████░░░░░░  1.204   %81                   │    │
│  │ Email  ███████████████░░░░░░░    987   %67                   │    │
│  │ ⓘ Sayılar kanal izni ve iletişim bilgisi olanlara göre.       │    │
│  └──────────────────────────────────────────────────────────────┘    │
│  [Brief'i düzenle]                               ( İçeriği üret ↗ )  │
```
Durumlar: skeleton (sayı bloğu + 2 çubuk), hazır, 0 kişi (uyarı + "İçeriği üret" pasif), 502. Yeniden üretim içerik varken `ConfirmDialog regenerate` (REQ-APR-03: içerik ve onay geçersiz olur).

### 8.3 İçerik (iki sütun ≥1024; solda editör/akış, sağda önizleme)

```
│ [● SMS ✓] [● Email ⋯]                                                 │
│ ┌ SMS gövdesi ─────────── [Taslak] ┐ ┌ Önizleme · örnek: Ayşe ─────┐ │
│ │ Merhaba {{first_name}}, sepetin… ▍│ │   ╭──────────────────────╮  │ │
│ │                                   │ │   │ Merhaba Ayşe, sepetin│  │ │
│ │ [+ first_name][+ last_name][+ city]│ │   │ seni bekliyor…       │  │ │
│ │                        142/160    │ │   ╰──────────────────────╯  │ │
│ └───────────────────────────────────┘ └─────────────────────────────┘ │
│  [Yeniden üret]                        ( Önizle ve onayla ↗ ) (pasif) │
```
Email sekmesinde: konu `Input` + sayaç `/80`, gövde `Textarea`; sağda `EmailPreview` (konu satırı + iframe). Akış sırasında editör `readonly`, caret görünür; her iki kanal `validated` olunca rozetler yeşile döner ve ana eylem aktifleşir. `interrupted` ve son `invalid` durumunda "Yeniden üret" birincil olur.

### 8.4 Onay

```
│ ┌ Onay özeti ──────────────────────────────────────────────────┐ │
│ │ Kitle  SMS 1.204 · Email 987                                  │ │
│ │ SMS    [Doğrulandı] 142/160   [Düzenle]                       │ │
│ │ Email  [Doğrulandı] konu 54/80 [Düzenle]                      │ │
│ │ ┌ SmsPreview ┐  ┌ EmailPreview (sandbox) ┐                    │ │
│ └──────────────────────────────────────────────────────────────┘ │
│ ═ ApprovalBar ══ ✓ İki kanal doğrulandı      ( Onayla ↗ ) ═══════ │
```
Onay sonrası bar: "Onaylandı" + ( Gönder ↗ ) → `ConfirmDialog send`. 422 (REQ-CNT-05 kişiselleştirme sonrası aşım) diyalog içinde ilgili kanal satırına bağlı hata olarak gösterilir. Düzenle → İçerik adımına döner; onaylıyken düzenleme onayı düşürür (`stale`).

### 8.5 Takip / Dashboard

```
│ Kampanya gönderiliyor                         [Gönderiliyor]      │
│ ┌ SMS ─────────────────────────┐ ┌ Email ───────────────────────┐ │
│ │ 1.204 alıcı                   │ │ 987 alıcı                    │ │
│ │ ███████████▓▓░░░░░░  %62      │ │ ██████▓░░░░░░░░░░░  %38      │ │
│ │ Gönderildi  742  Başarısız  9 │ │ Gönderildi 371  Başarısız 4  │ │
│ │ Gönderiliyor 12  Kuyrukta 441 │ │ Gönderiliyor 8  Kuyrukta 604 │ │
│ └───────────────────────────────┘ └──────────────────────────────┘ │
│ ┌ Canlı akış ─────────────────────────────────────────────────┐   │
│ │ 14:02:11 ● SMS   [Gönderildi]  müşteri #4821                 │   │
│ │ 14:02:11 ● Email [Başarısız]   müşteri #0193 · 5. deneme     │   │
│ └──────────────────────────────────────────────────────────────┘   │
```
`completed`: başlık "Kampanya tamamlandı", rozet `Tamamlandı`, başarısız sayısı sıfır değilse `--error` vurgulu ve "Başarısızları gör" listesi (kayıt alanları sözleşmeye bağlı). Kampanya düzeyinde "Başarısız" durumu gösterilmez (REQ-TRK-01).

### 8.6 Mobil (<768)

- Stepper kompakt: "Adım 3/5 · İçerik" + 5 segment çubuk; dokununca adım listesi `Sheet`.
- İçerik ekranı tek sütun: editör → önizleme sekmesi (`Düzenle | Önizle` segmentli kontrol) yerine dikey sıra; `ApprovalBar` ekran altına yapışık (safe-area padding), birincil buton tam genişlik.
- Dashboard kanal kartları alt alta; feed son 20 olay.
- Dokunma hedefleri ≥ 44×44 px; kart iç boşluğu 16; display 32 px.

## 9. Erişilebilirlik

- **Odak:** her etkileşimli öğede `:focus-visible` → `--shadow-focus` (2 px boşluk + 2 px `--orange-600`, açık zeminlerde ≥3:1). Odak halkası asla `outline: none` ile silinmez.
- **Akan metin:** `StreamingText` kapsayıcısı `aria-live="off"` (her delta okunmaz); ayrı, görsel olarak gizli bir `role="status"` bölgesi yalnızca durum değişimlerini okur: "SMS içeriği üretiliyor", "SMS içeriği doğrulandı", "Email konusu geçersiz: 83 karakter, sınır 80". Akış boyunca kapsayıcı `aria-busy="true"`.
- **Sayaçlar:** `CharCounter` `aria-live="polite"` yalnızca eşik değişiminde (normal → near → over) duyurur; her tuşta değil. Alan `aria-describedby` ile sayaca ve hata listesine bağlı; over-limit'te `aria-invalid="true"`.
- **Dashboard:** `DeliveryCounters` her SSE güncellemesinde değil, en fazla 10 sn'de bir `polite` özet ("SMS: 742 gönderildi, 9 başarısız"); `ActivityFeed` `role="log"`, `aria-live="off"` (yoğun akış ekran okuyucuyu boğmasın), tamamlanma `assertive` değil `polite` duyurulur.
- **Klavye akışı:** Brief textarea → gönder (⌘/Ctrl+Enter) → Kitle kartı → "İçeriği üret" → sekmeler (← →, Reka Tabs) → editör → değişken chip'leri → onay. Adım değişince odak yeni ekranın `h1`'ine taşınır (`tabindex="-1"`). Dialog odak tuzağı ve Esc (Reka); kapanınca odak tetikleyiciye döner.
- **Renkten bağımsız durum:** her durum ikon + metin taşır; segmentli çubuğun altında sayılar metin olarak yazılıdır; kanal ayrımı renk + etiket ("SMS"/"Email").
- **Pasif onay:** `ApprovalBar` butonu `aria-disabled="true"` (odaklanabilir kalır) ve `aria-describedby` ile neden metnine bağlıdır.
- **LLM metni:** yalnızca metin interpolasyonu; `v-html` yasak (ESLint `vue/no-v-html: error` — ÖNERİ). Email önizleme iframe'i `sandbox=""` (hiçbir izin yok) + `srcdoc` içinde kaçışlanmış metin, `title="Email önizlemesi"`; srcdoc'a `<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'">` (ÖNERİ). REQ-UI-01.
- **Dil:** `<html lang="tr">`; uppercase dönüşümleri Türkçe kurallarla.
- **Hareket:** §5 reduced-motion kuralları; yanıp sönen caret dışında 3 Hz üzeri yanıp sönme yok.

## 10. Frontend konvansiyonları (ÖNERİ — bağlayıcı değil)

**Klasör düzeni:**
```
web/src/
  app/            App.vue, router.ts, providers (Toaster)
  pages/          BriefPage, AudiencePage, ContentPage, ApprovalPage, TrackingPage
  components/ui/  shadcn-vue çıktıları (CLI üretir, elle minimum değişiklik)
  components/     domain bileşenleri (§7 custom olanlar)
  composables/    useEventStream, useCharCount, useAnnouncer
  stores/         config, campaign, audience, content, delivery
  api/            fetch istemcisi + tipler (sözleşmeden)
  i18n/tr.ts      tüm metinler, id → string
  styles/         tokens.css, base.css
```

**Durum (Pinia, alan başına bir store):** `config` (etkin LLM sağlayıcısı, REQ-AI-03) · `campaign` (kimlik, durum makinesi durumu; stepper buradan türetilir) · `audience` (kural, sayılar) · `content` (kanal/alan başına `{ text, phase: idle|streaming|retrying|validated|invalid|interrupted, errors[], versionId }`) · `delivery` (sayaçlar, feed halka tamponu 50). Store'lar sunucuyu tek doğruluk kaynağı sayar; 409 sonrası ilgili store yeniden çekilir, iyimser güncelleme yok.

**SSE composable `useEventStream`:** girdi URL/istek + olay işleyicileri; çıktı `status: idle|connecting|open|closed|error`. Bağımlılık enjeksiyonu: `EventSource` fabrikası parametre olarak verilir (test için sahte). Kurallar: (1) son olay gelmeden kapanan akış → `interrupted`; (2) son olmayan `invalid` → ilgili alan metni temizlenir, `retrying`; (3) bileşen unmount'ta bağlantı kapanır; (4) dashboard akışı kopunca üstel yeniden bağlanma (1 s → 30 s), içerik akışında otomatik yeniden bağlanma **yok** (yeniden üretim kullanıcı kararı). İçerik üretimi POST ile başlatılıyorsa `EventSource` yetmez; `fetch` + `ReadableStream` SSE ayrıştırıcısı gerekir — sözleşmeye bağlı (açık soru).

**Yükleniyor/hata:** her async eylem `{ state: 'idle'|'loading'|'success'|'error', error?: ApiError }` döner; `ApiError` HTTP kodu + sözleşmedeki hata gövdesi. Eşleme §7 tablosundaki tek fonksiyonda (`errorPresentation(code)`). Skeleton yalnızca ilk yüklemede; yeniden yüklemede eski veri + küçük spinner.

**Metin:** tüm UI metni `i18n/tr.ts` içinde id ile (`brief.submit`, `audience.consentNote` …); bileşende çıplak Türkçe string yok. Kütüphane: başlangıçta tipli düz nesne + `t(id, params)`; İngilizce eklenirse `vue-i18n`'e geçiş (VARSAYIM).

**Test (Vitest + Vue Test Utils):** seçiciler **yalnızca** `[data-testid="…"]` (id'ler `docs/contracts/ui.md`'den; metin/sınıf seçici yok). Sahte `EventSource` ile delta → caret görünür, `validated` → onay aktif, son olay yok → onay pasif; `CharCounter` 160/161 ve 80/81 sınırları (Türkçe karakter dahil); `<script>` içeren içerik metin olarak görünür ve DOM'da `script` öğesi oluşmaz; "Mock LLM" rozeti `config` store'una göre. Zamanlayıcılar `vi.useFakeTimers()`. Bunlar PLAN §4.4 "Frontend" satırıyla örtüşür; testleri yalnızca test yazarı ajanı yazar.

## 11. Açık sorular ve VARSAYIM listesi

**Açık sorular — karara bağlandı (2026-10-26):**
1. Onay öncesi en uzun uzunluk (REQ-CNT-05) API'den döner: her içerik sürümü ve `validated` olayı `worst_case {sms_runes, subject_runes}` taşır; `CharCounter` ikinci satırı gösterir (`docs/contracts/ui.md`).
2. Önizleme varsayılan olarak eşleşen ilk alıcıyı gösterir; "sonraki örnek" kontrolü `sample_index` ile ilerler.
3. İçerik üretimi **POST + fetch tabanlı akış** (EventSource değil); canlı durum GET + EventSource, otomatik yeniden bağlanma.
4. `ActivityFeed`, sayaçlar değiştiğinde en fazla 2 sn'de bir `deliveries?order=recent` sorgusuyla beslenir.
5. E-posta gövdesi düz metin.
6. Turuncu metin preset'i: `accent` (varsayılan; sahip itiraz etmedi).
7. Dark tema: teslimde zorunlu değil, opsiyonel (P6).
8. Kampanya listesi / geçmiş ekranı: kapsam dışı (tek kampanya akışı).

**VARSAYIM'lar:**
- V1. Arayüz dili Türkçe; tüm metinler tek i18n dosyasında id ile; İngilizce sonradan eklenebilir.
- V2. Font Figtree + JetBrains Mono, self-host; referans fontu kesin teşhis edilmedi.
- V3. Hata rengi crimson `#B4233C`, marka turuncusundan ayrışacak şekilde seçildi; referansta hata rengi yok.
- V4. CTA gradient'i erişilebilirlik için referanstan (`#FA5B23→#EA2D2B`) koyulaştırıldı (`#D63A1E→#C42A26`).
- V5. Near-limit eşiği %90 (SMS 144, konu 72); brief için 1800/2000.
- V6. Karakter sayımı Unicode code point (`[...s].length`), backend'in rune sayımıyla aynı; emoji ZWJ dizileri birden çok sayılır.
- V7. Teslimat sayaç duyuruları en fazla 10 sn'de bir; feed tamponu 50 olay (mobil 20).
- V8. Kanal renkleri SMS teal `#0E7C7E`, Email indigo `#3B5BDB` (referansta karşılığı yok).
- V9. Grafik kütüphanesi kullanılmaz; çubuklar custom.
- V10. Varsayılan turuncu metin preset'i `accent`.
- V11. İçerik akışında otomatik yeniden bağlanma yok; dashboard akışında var.
- V12. Kenar değerleri (radius, gölge, dark tema) approximated; ilk ekran görüntüsünden sonra sahiple gözden geçirilir.
