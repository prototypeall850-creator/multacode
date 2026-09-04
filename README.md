# MultaCode

TUI coding agent untuk Termux — asisten coding terminal-first berbasis Go,
terinspirasi OpenCode. Ngobrol dengan LLM, rujuk file, biarkan agent
menginspeksi/mengedit kode, jalankan shell dengan konfirmasi, dan pindah
antara mode planning yang aman dan mode build. Sesi tersimpan lokal.

![status](https://img.shields.io/badge/status-milestone%200--6%20selesai-green)
![go](https://img.shields.io/badge/go-1.27-blue)

## Instalasi

Syarat: Go 1.27+ (Termux: `pkg install golang git`).

```sh
git clone <url-repo-kamu> ~/multacode
cd ~/multacode
go build -o multacode ./cmd/multacode   # 1. wajib: binary tidak ikut di-clone
```

Biar bisa dipanggil dari mana saja (Termux):

```sh
ln -s "$PWD/multacode" $PREFIX/bin/multacode   # 2. wajib (Termux)
hash -r
```

Di Linux umum (bukan Termux), ganti langkah 2 dengan:

```sh
ln -s "$PWD/multacode" ~/.local/bin/multacode  # pastikan ~/.local/bin ada di PATH
```

Lalu setup sekali seumur hidup, pakai dari folder mana pun:

```sh
multacode --setup   # 3. cukup sekali: bikin config global
multacode           # 4. pakai dari folder mana pun (cd ~/1, ~/2, ...)
```

> `--setup` menyiapkan config global ikut standar XDG
> (`~/.config/multacode/`, `~/.local/share/multacode/`) —
> **cukup sekali**, berlaku untuk semua folder kerja.
> Folder kerja tinggal `cd` + ketik `multacode`, tanpa setup ulang.

Tanpa daemon, tanpa Docker, tanpa Node/Bun. Jalan di terminal sempit (40 kolom).

## Mulai cepat

1. Tambah provider: di dalam TUI ketik `/connect new` (Zen, Anthropic,
   atau endpoint apa pun yang kompatibel OpenAI), atau jalan pintas untuk
   yang sudah ada: `/connect <id> <api-key>`. Key tersimpan di `auth.json`,
   tidak pernah masuk log chat.
2. Pilih model: `/models` (daftar live + picker) atau `/models zen/glm-4.7-free`.
3. Ngobrol. Lampirkan konteks dengan `@file`, jalankan perintah dengan `!ls`.
4. Aksi mutasi selalu minta izin dulu: tekan `y` untuk setuju, `n` untuk tolak.

## Fitur (Milestone 0–6)

| Area | Isi |
|---|---|
| Chat TUI | Transkrip Bubble Tea, output streaming, spinner, help (`ctrl+h`), layout ramah Termux sempit |
| Provider | OpenAI-compatible (SSE chat + responses, tool call), Anthropic native, preset OpenCode Zen; `/connect`, `/models` |
| Agent loop | ReAct berbatas (16 langkah) dengan tool call, mode `build` vs `plan` (tab), eksekusi berizin |
| Tool | `list/search/read` file (`rg` kalau ada), `run_shell` dengan timeout, `web_search` (Tavily/Brave/SearXNG), `web_fetch` dengan guard SSRF, `edit_file` dengan preview diff |
| Keamanan | Kebijakan `allow/ask/deny`, modal approval (diff untuk edit), shell destruktif ditolak, path secret diblokir, secret disensor |
| Konteks | `@file` lampirkan file, output `!cmd`, `SOUL.md` + `multa.md` (project & global), profil env Termux masuk system prompt |
| Web | `/search`, `/fetch`, tracking `/sources`, prompt mewajibkan sitasi |
| Sesi | Auto-save tiap turn, picker resume `/sessions` (+ hapus), `/new`, pruning `/compact` |
| Operasional | `/doctor` (toolchain, konektivitas provider, sesi), hint error yang actionable, `/permissions`, `/soul`, `/memory` |

## Tombol & slash

- `enter` kirim · `alt+enter`/`ctrl+j` baris baru · `tab` build/plan ·
  `ctrl+r` detail tool · `ctrl+h` help · `esc` tutup · `ctrl+c` batal, 2× keluar
- Di modal approval: `y`/`enter` setujui sekali · `n`/`esc` tolak
- `/help /connect /models /sessions /new /agent /permissions /soul /memory`
  `/search /fetch /sources /compact /doctor /exit`

## Tata letak

```
~/.config/multacode/            config.json, SOUL.md, multa.md
~/.local/share/multacode/       auth.json, sessions/*.json
~/.cache/multacode/
```

| Path | Kode |
|---|---|
| `cmd/multacode` | Entrypoint CLI |
| `internal/tui` | Aplikasi Bubble Tea, picker, modal approval |
| `internal/agent` | Loop ReAct, prompt stack (soul/memory/env), pemetaan policy |
| `internal/provider` | OpenAI-compatible, Anthropic, preset Zen |
| `internal/tools` | Tool file/shell/web/edit, sensor secret |
| `internal/permission` | allow/ask/deny + klasifikasi risiko shell |
| `internal/session` | Persistensi JSON, metadata picker |
| `internal/config`, `internal/env` | Path XDG, auth, profil Termux |

## Contoh config

`~/.config/multacode/config.json`:

```json
{
  "default_provider": "zen",
  "default_model": "glm-4.7-free",
  "providers": [
    {"id": "zen", "kind": "zen", "api_key_ref": "auth:zen",
     "default_model": "glm-4.7-free"}
  ],
  "permission": {"read": "allow", "search": "allow", "edit": "ask",
                 "shell": "ask", "delete": "ask"},
  "search": {"provider": "tavily", "api_key_ref": "env:TAVILY_API_KEY"}
}
```

## Dev

```sh
go vet ./...    # harus bersih
go test ./...   # semua paket pass
```

## Lisensi

TBD — pilih satu (MIT/Apache-2.0) sebelum dipublish.
