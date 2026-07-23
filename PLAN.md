# propq — Plan

## Vizyon
Çoklu MySQL/MariaDB sunucusuna **tek bir komutla**, **asenkron** ve **hızlı** SQL göndermek için Go CLI aracı.  
CLI-first: pipe, arg, file, editor — hepsi birinci sınıf vatandaş.

---

## 1. CLI Tasarımı

```
propq [flags]

SQL kaynağı (sadece biri):
  --sql QUERY          Argüman olarak SQL
  -f, --file FILE      Dosyadan SQL oku (- için stdin, aynı)
  -e, --edit           $EDITOR aç, SQL yaz (:wq ile gönder)

Hedef filtreleme:
  -s, --server REGEX   Sunucu adı regex filtresi
  -d, --dbfilter REGEX Veritabanı adı regex filtresi
  --exclude-db REGEX   Hariç tutulacak DB regex

Davranış:
  --timeout SEC        Query timeout (default: 30)
  --concurrency N      Global concurrency override (default: config'deki max_connections)
  --force              Destructive SQL onayını atla
  --dry-run            Sadece hedefleri göster, SQL çalıştırma
  --no-transaction     Autocommit modu (transaction yok)

Çıktı:
  --json               JSON çıktı (AI/script dostu)
  --table              Zorla tablo çıktısı
  -q, --quiet          Progress bar gizle

Diğer:
  -c, --config FILE    Config yolu (default: ./propq.toml → ~/.config/propq/config.toml)
  --help, --version
```

### SQL kaynağı seçimi (öncelik sırası):
1. `--sql` flag'i varsa → onu kullan
2. `-f` flag'i varsa → dosyadan oku (`-f -` = stdin)
3. `-e` flag'i varsa → $EDITOR aç
4. stdin **pipe** ise (non-TTY) → stdin'den oku
5. Hiçbiri yoksa → hata göster + --help

### Kullanım örnekleri:
```bash
# En hızlı: tek satır SQL
propq --sql "SELECT COUNT(*) FROM users" --server prod-*

# Pipe ile (CLI-first!)
cat query.sql | propq -s staging

# Dosyadan + filtre
propq -f migration.sql -d "^shop_" --exclude-db "backup"

# Editor modu (db-runner tarzı)
propq -e

# AI dostu JSON çıktı
propq --sql "SHOW TABLES" --server prod-* --json

# Sadece hedefleri gör
propq --sql "..." -s prod-* --dry-run --table
```

---

## 2. Config Formatı (TOML)

```toml
# propq.toml
[defaults]
timeout = 30
concurrency = 5

[connections.prod-eu-1]
host = "db1.example.com"
port = 3306
user = "myuser"
password = "sifre123"
max_connections = 3
tags = ["prod", "eu"]

[connections.prod-eu-2]
host = "db2.example.com"
port = 3306
user = "myuser"
password = "sifre456"
max_connections = 2
tags = ["prod", "eu"]

[connections.staging-main]
host = "staging.example.com"
port = 3306
user = "testuser"
password = "testpass"
# max_connections default: 3
# tags default: []
```

**Config arama sırası:**
1. `-c` ile verilen path
2. `./propq.toml` (cwd)
3. `~/.config/propq/config.toml`

---

## 3. Proje Yapısı

```
propq/
├── cmd/
│   └── propq/
│       └── main.go            # Binary entry point: sadece app.Execute()
├── internal/
│   ├── app/
│   │   ├── run.go             # Ana orchestrator (config → scan → filter → execute → display)
│   │   └── flags.go           # Cobra command + flag tanımları
│   ├── config/
│   │   └── config.go          # TOML parsing, Connection yapısı, arama mantığı
│   ├── scanner/
│   │   └── scanner.go         # SQL kaynağı: flag / file / pipe / editor
│   ├── runner/
│   │   └── runner.go          # Async executor: goroutine pool, semaphore, results channel
│   └── display/
│       └── display.go         # Result formatting: table, json, summary
├── go.mod
├── Makefile                    # build, test, lint, install
├── LICENSE                     # MIT
├── README.md                   # Kullanım, kurulum, örnekler
├── .gitignore
└── propq.toml.example          # Örnek config
```

---

## 4. Veri Akışı

```
┌────────────┐    ┌──────────┐    ┌──────────┐    ┌───────────┐    ┌──────────┐
│  scanner   │ →  │  config  │ →  │  runner  │ →  │  display  │ →  │  stdout  │
│ (SQL al)   │    │ (bağlantı)│   │ (exec)   │    │ (formatla)│    │          │
└────────────┘    └──────────┘    └──────────┘    └───────────┘    └──────────┘
                       ↓
                 ┌────────────┐
                 │  filter    │
                 │ (server/db)│
                 └────────────┘
```

Detaylı akış:
1. **scanner** → SQL string'ini bir kaynaktan al (arg/file/pipe/editor)
2. **config** → TOML'dan connection'ları yükle
3. **filter** → --server / --dbfilter / --exclude-db regex uygula:
   - `--server` regex → connection isimlerini filtrele
   - Kalan her connection'a `SHOW DATABASES` at (async)
   - Gelen DB listesine `--dbfilter` / `--exclude-db` uygula
   - Kalan (server, db) çiftlerini runner'a ver
4. **runner** → her (server, db) çifti için goroutine:
   - Per-server semaphore (max_connections veya --concurrency)
   - SQL bağlan → execute → sonuç topla
   - Hata durumunda result'ta işaretle, stop-on-error varsa diğerlerini iptal et
5. **display** → sonuçları formatla:
   - Terminal ise (TTY) → renkli tablo + özet panel
   - `--json` varsa → JSON array
   - `--quiet` varsa → sadece özet
   - AI kullanımı için default JSON bile olabilir... (bunu tartışalım)

---

## 5. Async Execution Modeli

```
 Runner                          DB Servers
┌─────────────────────┐
│  Server Semaphores   │
│                     │
│  prod-eu-1: sem(3)──┼───────► DB1 ──► DB2 ──► DB3
│  prod-eu-2: sem(2)──┼───────► DB4 ──► DB5
│  staging:   sem(3)──┼───────► DB6 ──► DB7
│                     │
│  ┌─ Result Channel ─┐
│  │  {server,db,     │
│  │   status,rows,   │
│  │   error,elapsed} │
│  └──────────────────┘
└─────────────────────┘
```

- Her server için ayrı **semaphore** (max_connections kadar eşzamanlı bağlantı)
- Tüm (server, db) çiftleri bir **worker pool**'a atılır
- `--concurrency` verilirse tüm server'lar için global semaphore kullan
- Her goroutine: connect → execute → result → disconnect
- Progress bar: completed / total
- `--stop-on-error` varsa: ilk hatada `context.WithCancel` ile diğerlerini iptal et

---

## 6. Kullanıcı Deneyimi Hedefleri

| Senaryo | Komut | Süre |
|---------|-------|------|
| Tüm prod DB'lerde SELECT | `propq --sql "SELECT 1" -s prod` | ~sn |
| Migration dosyasını çalıştır | `propq -f migrate.sql -d "^shop_"` | ~sn |
| Pipe ile hızlı sorgu | `echo "SELECT COUNT(*)" \| propq` | ~sn |
| Editor ile SQL yaz + filtrele | `propq -e` | interaktif |
| AI aracından JSON al | `propq --json -s prod --sql "..."` | ~sn |
| Sadece hedefleri gör | `propq --dry-run -s prod --table --sql "..."` | ~sn |

---

## 7. Kullanılacak Go Kütüphaneleri

| Amaç | Kütüphane |
|------|-----------|
| CLI framework | `github.com/spf13/cobra` |
| TOML config | `github.com/pelletier/go-toml/v2` |
| MySQL driver | `github.com/go-sql-driver/mysql` |
| Renkli çıktı | `github.com/fatih/color` |
| Tablo çıktı | `github.com/olekukonko/tablewriter` veya stdlib `text/tabwriter` |
| Progress bar | `github.com/schollz/progressbar/v3` |

---

## 8. Yapılacaklar (sıralı)

1. **Proje iskeleti** — go.mod, cmd/propq/main.go, internal/ yapısı, Makefile
2. **config** — TOML parsing, config arama, Connection yapısı
3. **scanner** — SQL kaynağı: flag / file / pipe / editor
4. **flags / app** — Cobra command, flag tanımları, --help
5. **runner** — Async executor: goroutine + semaphore + context cancellation
6. **display** — Tablo + JSON + özet çıktı
7. **Entegrasyon** — Hepsini birleştir, `propq --sql "SELECT 1"` çalışır hale getir
8. **CLI iyileştirmeleri** — Destructive SQL uyarısı, dry-run, progress bar, renkler
9. **README + LICENSE** — Dokümantasyon, kurulum talimatları, örnekler
10. **GitHub hazırlık** — .gitignore, repo init, ilk commit

---

## 9. Sorular / Tartışma

1. **Varsayılan çıktı formatı ne olsun?** TTY'de renkli tablo, pipe'da JSON (otomatik dedection) — yoksa her zaman --json ile mi kontrol etsek?
2. **--dbfilter olmadan çalıştırınca ne olsun?** Tüm DB'lere mi göndersin, yoksa "Çok fazla DB var, --dbfilter kullan" uyarısı mı versin? Yoksa çok DB varsa --dry-run gibi bir safety check mi?
3. **Destructive SQL koruması** — db-runner'daki gibi "YES" yazma onayı mı, yoksa --force yoksa direkt red mi? Yoksa --force default'ta çalışsın mı (CLI-first, ben ne dersem onu yapar)?
4. **Editor modu** — sadece SQL için mi, yoksa DB seçimi için de editor açılsın mı? db-runner'da ikisi de vardı. Sen "aklında daha kolay bişey varsa onu yap" dedin.

---

Ne düşünüyorsun? Özellikle **sorular** kısmındakileri cevaplarsan planı netleştirip kodlamaya başlayabiliriz.
