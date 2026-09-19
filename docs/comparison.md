> También en español → [comparativa.md](comparativa.md)

# Splitstream compared with Restream, Castr, nginx-rtmp and MediaMTX

## 1. What this document compares, and what it does not

**Written on 2026-09-13.** Prices and plans of paid services change often: the ones here
are what was published on that date, and they have to be checked on each product's pricing
page before any decision is made on them.

This document is written by the authors of Splitstream. That is why it makes no value
judgements of our own: it does not say which one is better, easier or more powerful. It
says what each one does, what it costs and where your video goes, and leaves the
comparison to the reader. Where it repeats someone else's judgement (the "Cost of entry"
column in §5 is the roadmap's), it says whose it is.

**When a fact cannot be verified in the product's public documentation, the cell says "not
documented".** It is not filled in by approximation or from memory. A "not documented" cell
means "check it yourself in the source", not "it does not exist".

The sources are at the end, one letter per product ([S], [R], [C], [N], [M]), and each
table cites them in its last column.

The five products are not the same kind of thing, and that conditions everything else:

- **Splitstream**, **nginx-rtmp** and **MediaMTX** are software you install and run
  yourself.
- **Restream** and **Castr** are cloud services with an account and a subscription.

---

## 2. What each one does

| Product | Relay to several destinations | Local recording | Platform title and chat | Web panel | Install | Where it runs | Source |
| --- | --- | --- | --- | --- | --- | --- | --- |
| **Splitstream** | Yes: N RTMP/RTMPS destinations at once, no transcoding; from OBS or from the browser camera | Yes: FLV on your disk, with segments, a cap in GB and retention | Title on Twitch, YouTube and Kick with the account connected; read-only chat on those three | Yes, inside the same binary | Single binary: Homebrew, winget, script or Docker | Your machine or your server | [S] |
| **Restream** | Yes, to the platforms in its catalogue | Not documented | Yes: unified chat and title changes on the destinations that allow it | Yes, web application | An account on their site; nothing to install | Their servers | [R] |
| **Castr** | Yes, to the platforms in its catalogue | Not documented | Not documented | Yes, web application | An account on their site; nothing to install | Their servers | [C] |
| **nginx-rtmp** (nginx `rtmp` module) | Yes: one `push` directive per destination | Yes: `record` directive, in FLV | No | No; it exposes a statistics page (`stat`) in XML with an XSL stylesheet | Compile nginx with `--add-module` | Your machine or your server | [N] |
| **MediaMTX** | Not as a feature of its own: it is done by launching `ffmpeg` from `runOnReady` | Yes: `record: yes`, in fMP4 or MPEG-TS | No | No; it has an HTTP control API and a metrics endpoint | Single binary | Your machine or your server | [M] |

Two notes on the two self-hosted ones that are not Splitstream:

- The nginx-rtmp `push` speaks RTMP, not RTMPS. Destinations that require TLS (Facebook,
  for instance) need a tunnel in front, such as `stunnel` [N].
- MediaMTX is a multi-protocol server (RTSP, RTMP, HLS, WebRTC, SRT). Forwarding to several
  platforms at once is solved with external processes, not with a list of destinations [M].

---

## 3. What it costs

| Product | Free plan | First paid plan | Source |
| --- | --- | --- | --- |
| **Splitstream** | The whole product. MIT licence | None. The cost is your machine and your upload: `bitrate × number of destinations` | [S] |
| **Restream** | They publish a free plan. Exactly what it includes (destinations, watermarks, hours) changes and is not reproduced here | Not documented as of this date; the price is on their pricing page | [R] |
| **Castr** | Not documented | Not documented as of this date; the price is on their pricing page | [C] |
| **nginx-rtmp** | The whole module. Two-clause BSD licence | None. The cost is your machine and your upload | [N] |
| **MediaMTX** | The whole product. MIT licence | None. The cost is your machine and your upload | [M] |

The three self-hosted ones charge nothing, but they are not free either: the upload comes
out of your connection. Streaming at 4 Mbps to three platforms is 12 Mbps of sustained
upload. The two cloud services upload once and do the multiplying themselves.

---

## 4. Where your video goes

| Product | Path | Source |
| --- | --- | --- |
| **Splitstream** | Your computer → the platforms | [S] |
| **Restream** | Your computer → their servers → the platforms | [R] |
| **Castr** | Your computer → their servers → the platforms | [C] |
| **nginx-rtmp** | Your computer or your server → the platforms | [N] |
| **MediaMTX** | Your computer or your server → the platforms | [M] |

If you install any of the three self-hosted ones on a rented VPS, your video goes through
that VPS. The difference with the cloud services is not that there is no middleman: it is
that you pick the middleman and you administer it.

---

## 5. Capabilities per platform

This matrix comes from the roadmap
[`superpowers/specs/2026-09-09-roadmap-mejoras.md`](superpowers/specs/2026-09-09-roadmap-mejoras.md)
§2 (in Spanish) and describes what **each platform** allows through its API, not what any
one product does. It is reproduced as it stands except for one cell: where the roadmap
rated YouTube's scheduling ("the best of all of them"), this says what its API offers. Its
"Cost of entry" column is the roadmap's judgement about the integration work, not an
assessment of the platforms.

| Platform | Live title | Scheduling | Chat | Cost of entry |
| --- | --- | --- | --- | --- |
| **Twitch** | Yes, straightforward | Not applicable (there is no "event") | EventSub over WebSocket, push | Low. Register an app and that's it |
| **YouTube** | Yes | Yes, with a scheduled-event API | Polling, expensive in quota | **High.** OAuth verification + the quota problem (§3) |
| **Kick** | Yes, official public API | Partial | Only through an inbound webhook | Medium. Requires a public URL (§4) |
| **Facebook** | Yes | Yes | Yes | **Very high.** Requires App Review and is only available with business verification, and may require signing additional contracts |
| **X** | Unlikely | — | — | No viable route at a reasonable cost |
| **TikTok** | Live access restricted to partners | — | — | No viable route |

What Splitstream does with that matrix today: title on Twitch, YouTube and Kick;
read-only chat on those same three (Kick only with the panel reachable at a public HTTPS
URL); Facebook, X and TikTok, restreaming only [S].

---

## Sources

- **[S] Splitstream** — this repository: [`README.md`](../README.md) and
  [`docs/manual-de-usuario.md`](manual-de-usuario.md) (in Spanish).
- **[R] Restream** — <https://restream.io> (product) and <https://restream.io/pricing>
  (prices).
- **[C] Castr** — <https://castr.io> (product and prices).
- **[N] nginx-rtmp** — <https://github.com/arut/nginx-rtmp-module> and its directives wiki
  <https://github.com/arut/nginx-rtmp-module/wiki/Directives>.
- **[M] MediaMTX** — <https://github.com/bluenviron/mediamtx> (the project README is its
  documentation).
