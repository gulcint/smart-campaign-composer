# Segment DSL

Kapsam: REQ-AUD-00, REQ-AUD-01, REQ-AUD-02, REQ-AUD-03, REQ-AUD-04, REQ-AI-01 (segment).
JSON şeması: [`schemas/segment-rule.schema.json`](schemas/segment-rule.schema.json). Go imzaları: [`go-interfaces.md` §segment](go-interfaces.md#internalsegment).

Segment kuralı, LLM'in (veya `POST /api/segments/count` çağıranın) ürettiği bir **ağaçtır**. Backend onu önce yapısal olarak çözer (`segment.Parse`), sonra anlamsal olarak doğrular (`segment.Validate`), sonra **parametreli** bir SQL parçasına çevirir (`segment.Compile`). LLM çıktısı hiçbir aşamada SQL metni olarak çalıştırılmaz; SQL metni yalnızca backend kodundaki sabit parçalardan oluşur, kuraldan gelen her değer bir `$n` parametresidir.

## 1. Yapı

```text
rule      := group
group     := {"all": [node, ...]}      // hepsi (AND)
           | {"any": [node, ...]}      // en az biri (OR)
           | {"not": node}             // değil
node      := group | condition
condition := {"field": <alan>, "op": <operatör>, "value": <değer>}   // is_null / is_not_null'da value yok
```

- Kök **her zaman bir grup**tur; çıplak koşul kök olamaz (`schema_violation`).
- Bir nesne ya grup (`all` | `any` | `not` anahtarlarından **tam olarak biri**) ya da koşuldur (`field`, `op`, isteğe bağlı `value`). Başka anahtar (ör. `sql`, `where`, `raw_sql`) veya karışık anahtarlar `schema_violation`dır (REQ-AUD-02).
- `all`/`any` dizisi boş olamaz (`empty_group`).

### 1.1 Sınırlar (VARSAYIM, requirements "Sabitler")

| Sınır | Değer | Tanım | Hata kodu |
|---|---|---|---|
| Azami derinlik | 3 | Kökten herhangi bir koşula giden yoldaki **grup** (`all`/`any`/`not`) sayısı. Kök grup + koşullar = derinlik 1. | `max_depth_exceeded` |
| Azami koşul sayısı | 10 | Ağaçtaki toplam koşul (yaprak) sayısı | `max_conditions_exceeded` |
| `in`/`not_in` dizi uzunluğu | 1..20 | | `invalid_value` |

Derinlik örnekleri: `{"all":[c]}` → 1; `{"all":[{"any":[c]}]}` → 2; `{"all":[{"any":[{"not":c}]}]}` → 3; bir seviye daha → 4 → red.

## 2. İzinli alanlar (kapalı liste)

Kural **yalnızca** bu alanları kullanabilir. Ad, e-posta, telefon, izin kolonları ve kimlikler **bilerek yoktur**: izin filtresi modelin değil sistemin işidir (§6).

| Alan | Tip | Kaynak | Not |
|---|---|---|---|
| `city` | string | `customers.city` | Tam eşleşme, büyük/küçük harf duyarlı. Seed'deki kanonik değerler: `İstanbul`, `Ankara`, `İzmir`, `Bursa`, `Antalya`, `Adana`, `Konya`, `Gaziantep`, `Kayseri`, `Eskişehir`. Değer listede olmak zorunda **değildir** (bilinmeyen şehir = 0 eşleşme). |
| `language` | enum | `customers.language` | `tr`, `en` |
| `loyalty_tier` | enum | `customers.loyalty_tier` | `bronze`, `silver`, `gold`, `platinum` |
| `last_cart_add_at` | timestamp (nullable) | `customer_stats_at($ref).last_cart_add_at` | Referans zamandan önceki en son `cart_add` olayı |
| `last_purchase_at` | timestamp (nullable) | `customer_stats_at($ref).last_purchase_at` | En son `purchase` olayı |
| `orders_30d` | integer | `customer_stats_at($ref).orders_30d` | `[ref − 30 gün, ref]` aralığındaki `purchase` sayısı |
| `cart_adds_30d` | integer | `customer_stats_at($ref).cart_adds_30d` | `[ref − 30 gün, ref]` aralığındaki `cart_add` sayısı |
| `total_orders` | integer | `customer_stats_at($ref).total_orders` | `ref`'e kadar tüm `purchase` sayısı |
| `total_spent` | number | `customer_stats_at($ref).total_spent` | `ref`'e kadar `purchase.amount` toplamı (TRY, 2 ondalık) |

`customer_stats_at(ref_time timestamptz)` bir SQL fonksiyonudur (bkz. [`db-schema.md` §3.3](db-schema.md#33-customer_stats_atref_time)); `ref`'ten **sonraki** olaylar hesaba katılmaz. Bu sayede sonuç yalnızca veriye ve enjekte edilen referans zamana bağlıdır.

## 3. Operatörler (tip başına)

| Tip | İzinli operatörler | `value` biçimi |
|---|---|---|
| string (`city`) | `eq`, `neq`, `in`, `not_in` | `eq`/`neq`: string, 1..100 karakter. `in`/`not_in`: 1..20 string'lik dizi |
| enum (`language`, `loyalty_tier`) | `eq`, `neq`, `in`, `not_in` | string, enum üyesi olmalı (yoksa `invalid_value`) |
| integer (`orders_30d`, `cart_adds_30d`, `total_orders`) | `eq`, `neq`, `gt`, `gte`, `lt`, `lte`, `between` | tam sayı, 0..1000000. `between`: `{"min": a, "max": b}`, tam sayılar, `a ≤ b`, iki uç dahil |
| number (`total_spent`) | `eq`, `neq`, `gt`, `gte`, `lt`, `lte`, `between` | sayı, 0..100000000. `between` aynı biçim |
| timestamp (`last_cart_add_at`, `last_purchase_at`) | `within_last`, `older_than`, `is_null`, `is_not_null` | `within_last`/`older_than`: `{"days": N}`, N tam sayı 1..3650. `is_null`/`is_not_null`: `value` yok veya `null` |

Hata önceliği (aynı koşulda tek hata raporlanır, sırayla ilk başarısız kontrol): `unknown_field` → `unknown_operator` → `operator_not_allowed` → `type_mismatch` → `invalid_value`.

- `unknown_field`: alan listede yok (ör. `email`, `phone`, `sms_opt_in`, `first_name`).
- `unknown_operator`: operatör §3'teki 13 operatörden biri değil (ör. `like`, `regex`, `raw`).
- `operator_not_allowed`: operatör geçerli ama alanın tipine izinli değil (ör. `city` + `gt`, `orders_30d` + `within_last`).
- `type_mismatch`: `value`'nun JSON tipi yanlış (ör. `orders_30d eq "0"`, `city eq 5`, `within_last` + `30`, `in` + string).
- `invalid_value`: tip doğru, değer kural dışı (aralık dışı, boş string, enum dışı, `min > max`, `days` 0, `is_null` ile değer, kesirli sayı integer alanda, dizi uzunluğu).

> **Not (JSON şeması ile Go doğrulayıcı ilişkisi):** `schemas/segment-rule.schema.json` **yapısal** şemadır: alan ve operatör adları için `enum` içermez (yalnızca `string`). Adları sınırlayan `enum`, modeli yönlendirmek için yalnızca `schemas/llm/segment-rule.tool.json` (strict varyant) içindedir. Go tarafında `segment.Parse` **yalnızca yapıyı** (bilinmeyen anahtar, grup/koşul karışıklığı, `field`/`op`'un string olmaması, eksik `field`/`op`) denetler ve `schema_violation` döner; alan ve operatör adlarının listede olup olmadığı `segment.Validate`'te ayrıntılı kodlarla (`unknown_field`, `unknown_operator`) raporlanır. Yani `{"field":"email",...}` bir `schema_violation` değil `unknown_field`dır; derinlik 4 bir `schema_violation` değil `max_depth_exceeded`dır.

## 4. Göreli zaman ve referans zaman

- "Son 30 gün" = `{"field":"last_cart_add_at","op":"within_last","value":{"days":30}}`.
- Anlamı: `ref − days×24h ≤ last_cart_add_at ≤ ref`. **Alt sınır dahildir**: tam 30×24 saat önceki olay eşleşir, 30×24 saat + 1 sn önceki eşleşmez. `NULL` eşleşmez.
- `older_than {"days": N}`: `last_x < ref − N×24h` (NULL eşleşmez).
- `ref` (referans zaman) **enjekte edilir**, SQL'de `now()` kullanılmaz:
  - Kampanya segmenti üretilirken `ref = SEGMENT_REFERENCE_TIME` (ortam değişkeni, RFC 3339; boş değilse) **yoksa** `Clock.Now()`. Bu değer kampanyaya `segment.reference_time` olarak yazılır ve gönderimde teslimat kayıtları **aynı `ref`** ile hesaplanır (sayım = teslimat sayısı). VARSAYIM.
  - Testler ve `compose.test.yml`: `SEGMENT_REFERENCE_TIME=2026-01-15T12:00:00Z` (seed `--ref-time` ile aynı).

## 5. Parametreli sorguya çeviri

`segment.Compile(rule, ref)` bir `Compiled{Where string; Args []any}` döner. `Where`, aşağıdaki sabit iskelete takılır:

```sql
SELECT c.id
FROM customers c
JOIN customer_stats_at($1) s ON s.customer_id = c.id
WHERE <Where>
```

Kurallar:

1. `$1` her zaman `ref`'tir (`Args[0] == ref`, `time.Time`, UTC).
2. Alan → SQL ifadesi eşlemesi **sabit tablodur** (`city` → `c.city`, `orders_30d` → `s.orders_30d`, …); alan adı asla kullanıcı/LLM metninden SQL'e kopyalanmaz.
3. Her değer yeni bir `$n` parametresidir (`$2`, `$3`, …, soldan sağa, derinlik öncelikli sırayla). `in` dizisi tek parametre olarak `= ANY($n)` (`text[]`) ile geçer.
4. `within_last` → `s.last_cart_add_at >= $1 - make_interval(days => $n) AND s.last_cart_add_at <= $1`.
5. `all` → `( a AND b )`, `any` → `( a OR b )`, `not` → `NOT ( a )`.
6. `Where` metninde **hiçbir kural değeri bulunmaz** (test: zararlı değer `Args` içinde aynen yer alır, `Where` içinde geçmez).

Örnek: kural `{"all":[{"field":"city","op":"eq","value":"İstanbul"},{"field":"orders_30d","op":"eq","value":0}]}` →

```text
Where: (c.city = $2 AND s.orders_30d = $3)
Args:  [2026-01-15T12:00:00Z, "İstanbul", 0]
```

(Boşluk biçimi sözleşme değildir; testler `Where`'i birebir karşılaştırmaz, yalnızca §5.6'yı ve `Args`'ı doğrular.)

## 6. Kanal izin filtresi (sistem uygular, model değil)

Kural bir **eşleşen küme** (`matched`) üretir. Kanal kitleleri sistem tarafından şu filtreyle türetilir:

| Kanal | Bir müşteri kanal kitlesine girer ancak ve ancak |
|---|---|
| `sms` | kuralla eşleşir **ve** `sms_opt_in = true` **ve** `phone IS NOT NULL` |
| `email` | kuralla eşleşir **ve** `email_opt_in = true` **ve** `email IS NOT NULL` |

- `audience.matched` = eşleşen müşteri sayısı (izinden bağımsız); `audience.sms`, `audience.email` = kanal kitleleri. (REQ-AUD-03)
- Gönderimde teslimat kayıtları **tam olarak** bu kanal kitlelerinden oluşur (REQ-AUD-04, REQ-SND-01). Aynı telefon/e-postayı paylaşan iki müşteri iki ayrı teslimat alır (REQ-SND-03).
- Kural izin alanlarını içeremez (§2), dolayısıyla modelin izni "açması" mümkün değildir.

## 7. Örnekler

### 7.1 Örnek brief kuralı (kanonik)

Brief: "Son 30 günde sepetine ürün ekleyip alışveriş yapmayan İstanbul'daki kullanıcılara %30 indirim hatırlatması gönder. Samimi ama kısa olsun."

```json
{
  "rule": {
    "all": [
      { "field": "city", "op": "eq", "value": "İstanbul" },
      { "field": "last_cart_add_at", "op": "within_last", "value": { "days": 30 } },
      { "field": "orders_30d", "op": "eq", "value": 0 }
    ]
  }
}
```

"Alışveriş yapmayan" = son 30 günde satın alma yok (`orders_30d = 0`) — **sahip kararı** (VARSAYIM olarak `DECISIONS.md`'ye yazılır). Diğer yorumu ("sepete eklemeden sonra satın almadı") ayırt etmek için seed'de `purchase_before_cart_add` grubu vardır (seed §3.3). Sahte LLM'in `ok` senaryosu tam olarak bu kuralı döner ([`llm-tools.md` §3](llm-tools.md#3-sahte-llm-senaryoları)) ve seed manifestinde `example_brief.rule` olarak aynen bulunur ([`seed.md`](seed.md)).

### 7.2 Geçerli diğer örnekler

```json
{"rule":{"any":[{"field":"loyalty_tier","op":"in","value":["gold","platinum"]},{"field":"total_spent","op":"between","value":{"min":1000,"max":5000}}]}}
{"rule":{"all":[{"field":"city","op":"neq","value":"Ankara"},{"not":{"field":"last_purchase_at","op":"is_null"}}]}}
```

### 7.3 Doğrulayıcı beklenen sonuçları (kötü niyetli / hatalı girdiler)

| # | Girdi (`rule` veya tüm gövde) | Sonuç | `path` | `code` |
|---|---|---|---|---|
| 1 | `{"rule":{"all":[{"field":"city","op":"eq","value":"'; DROP TABLE customers; --"}]}}` | **Geçerli**; değer yalnızca parametre; `matched = 0`; tablolar yerinde | — | — |
| 2 | `{"rule":{"all":[...]},"sql":"SELECT * FROM customers"}` | Red | `sql` | `schema_violation` |
| 3 | `{"rule":{"where":"1=1"}}` | Red | `rule.where` | `schema_violation` |
| 4 | `{"rule":{"all":[{"field":"email","op":"eq","value":"a@example.com"}]}}` | Red | `rule.all[0].field` | `unknown_field` |
| 5 | `{"rule":{"all":[{"field":"sms_opt_in","op":"eq","value":true}]}}` | Red | `rule.all[0].field` | `unknown_field` |
| 6 | `{"rule":{"all":[{"field":"city","op":"like","value":"%stan%"}]}}` | Red | `rule.all[0].op` | `unknown_operator` |
| 7 | `{"rule":{"all":[{"field":"city","op":"gt","value":"A"}]}}` | Red | `rule.all[0].op` | `operator_not_allowed` |
| 8 | `{"rule":{"all":[{"field":"orders_30d","op":"eq","value":"0"}]}}` | Red | `rule.all[0].value` | `type_mismatch` |
| 9 | `{"rule":{"all":[{"field":"orders_30d","op":"eq","value":"0 OR 1=1"}]}}` | Red | `rule.all[0].value` | `type_mismatch` |
| 10 | `{"rule":{"all":[{"field":"last_cart_add_at","op":"within_last","value":{"days":0}}]}}` | Red | `rule.all[0].value` | `invalid_value` |
| 11 | `{"rule":{"all":[{"field":"language","op":"eq","value":"de"}]}}` | Red | `rule.all[0].value` | `invalid_value` |
| 12 | `{"rule":{"all":[{"any":[{"not":{"all":[{"field":"city","op":"eq","value":"İzmir"}]}}]}]}}` (derinlik 4) | Red | `rule.all[0].any[0].not` (4. seviye grup) | `max_depth_exceeded` |
| 13 | 11 koşullu `all` | Red | `rule` | `max_conditions_exceeded` |
| 14 | `{"rule":{"all":[]}}` | Red | `rule.all` | `empty_group` |
| 15 | `{"rule":{"field":"city","op":"eq","value":"İstanbul"}}` (çıplak koşul kök) | Red | `rule` | `schema_violation` |
| 16 | `{"rule":{"all":[{"field":"city","op":"eq","value":"İzmir","all":[]}]}}` | Red | `rule.all[0]` | `schema_violation` |
| 17 | `{"rule":{"all":[{"field":"total_spent","op":"between","value":{"min":10,"max":5}}]}}` | Red | `rule.all[0].value` | `invalid_value` |
| 18 | Geçersiz JSON metni (`{"rule": {"all": [ ,]}}`) | Red | `` (boş) | `invalid_json` |

Path biçimi: noktalı alan adları + köşeli parantezli dizin, kök `rule` (gövde düzeyindeki bilinmeyen anahtar için anahtarın kendisi, ör. `sql`). Derinlik hatası, sınırı aşan **ilk düğümün** path'ini taşır; koşul sayısı hatası `rule` path'ini taşır.

Bir kural birden çok hata içerebilir; `Validate` hepsini toplar (sıra: derinlik-öncelikli gezinme sırası). `max_conditions_exceeded` ve `max_depth_exceeded` kontrolleri alan/operatör kontrollerinden **önce** yapılır ve bunlardan biri varsa diğer kontroller atlanır (büyük girdilerde maliyet sınırı).
