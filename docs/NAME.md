# Product name research (2026-09-03) — for the founder's decision (pm/LAUNCH.md F1)

Three research lenses (collisions/availability, friend-side read, fresh generation) and a synthesis;
journal `wf_a5d23ba8-16d`. Verified facts are marked; **no trademark clearance was performed on any
recommended name** — that is counsel's job before the mark goes on a public post.

## The finding that moves the decision

**BunnyWay d.o.o. (bunny.net) holds EU trade mark 018795266 on the bare word BUNNY, Class 42**
(registered 2023-03-01): "web hosting services; SaaS; PaaS; electronic data storage; cloud computing
consulting" — a description of a self-hosted AI server product. The existing Bunny apps are Class 9
desktop utilities at a defensible distance; a Bunny-prefixed *server/share/host* product walks into the
registered scope. No suffix cures it. **Verdict: this product stands alone; no Bunny prefix.**
(Open question for counsel, not assessed: whether the Class 9 family itself is exposed.)

## The structural test

"Paste this code into <name>" — you paste into a *place*. That eliminates artifacts (Invite, Guestlist),
gestures (Doorbell), verbs (Lend), roles (Host), and names with the wrong direction (Housecall: the
expert travels; Openhouse: ungated). Infrastructure words (network, hub, mesh, tunnel, torrent) are out
by the founder's ruling and by the SEO research (they pull zero search and frighten friends).

## Shortlist

| Name | Binary / npm / brew | Domains (existence only, 2026-09-03) | Why | Risk |
|---|---|---|---|---|
| **Guestroom** | free / free / free | .com held (web agency redirect), .app parked, .ai thin placeholder — none a competing product | The right place: kept made up on your property, entered by invitation, used, left. A stranger reconstructs the product from the name + one sentence. | Common noun → suggestive mark, harder to defend; domain purchase or TLD choice needed |
| **Hutchling** | free / free / free | .com/.app/.ai all unregistered (RDAP 404) | Cleanest namespace found; coined = strongest registrable position; rabbit world without "Bunny" | Collapses to "Hutch" in speech/search/shell — and hutchdb.com is a live self-hostable AI-agent workspace (verified) on top of a US Class 9 HUTCH mark (Hutch Games) |
| Porchlight | free / free / free | .com/.app/.ai registered; theporchlight.ai is an AI agency | Best felt meaning: the light left on because someone is coming | No familiar-TLD domain; 10 chars; nonprofit long tail |
| Guesthouse | npm TAKEN | .com since 1995; .ai created 2026-06-17 | Most legible to a stranger | Worst availability of the viable names |
| Stoop | npm TAKEN | .com/.ai registered | Best pure binary/prefix (5 letters) | Two small consumer apps; regional US vocabulary |
| Bunny Circle | 11 chars | .com for sale; .app/.ai unregistered | Only if the prefix is kept over the verdict | Inside the EUIPO BUNNY registration regardless |

## Rejected (with reasons)

Bunny Network (registered-mark collision, plus the SEO do-not-ship) · taillama (Meta's Llama naming
rules + Tailscale's "tail-" family) · Bunny Host/Hub/Link/Share/Invite (inside bunny.net's own service
vocabulary, or name the artifact not the place) · Burrow (`brew install burrow` is LinkedIn's Kafka
tool) · Hutch bare (hutchdb.com + US mark) · Warren (warren.io cloud platform; a warren is literally a
tunnel network) · Sidecar (Apple feature on this platform) · Porch (Nasdaq PRCH; "porch pirate") ·
Token Torrent (founder: many-to-many metaphor, negative association, names the transport).

## PM recommendation

**Guestroom**, with Hutchling as the fallback if the domain purchase or clearance fails. Reasoning in
the message to the founder of 2026-09-03. Both need: a trademark knock-out search in Classes 9/42 (US
+ EU), a domain decision (buy guestroom.app/.ai, or a path on an owned domain), and the CLI binary name
(`guestroom`, alias `gr`).

---

# Round 2 (2026-09-03): names in the audience's own vocabulary

The founder rejected round 1 ("none of the names describe the product"). Round 2 generated from the
LM Link market vocabulary (docs/MARKET-LMLINK.md: "use my local models remotely", "my model", "API key",
"access", "remote client", "self-hosted"; the audience never says "friend"/"invite"/"guest"), then
collision-checked every candidate individually (15 agents; journal `wf_9f974100-bcd`). Legibility =
"a LocalLLaMA reader knows what it does from the name".

## Shortlist

| Name | Binary | Legibility | Availability (verified 2026-09-03) | The catch |
|---|---|---|---|---|
| **Model Key** | `modelkey` | 5 | npm/PyPI/brew/Docker free; **github.com/modelkey free**; .app/.io unregistered; .com for sale (BuyDomains); .ai/.dev held by an unknown party (GoDaddy placeholder, "coming soon", paid to 2028) | Under-describes (says key-to-a-model, not local/shared); copy must say "invite code" not "key"; a live US Class 28 (toys) MODELKEY mark; likely a weak (descriptive) mark |
| Model Access | `modelaccess` | 5 | .ai FREE; github org free; registries free; .com parked | AWS Bedrock's console page is literally "Model access"; reads as provider-side entitlement — the intermediary the audience refuses; SEO unwinnable |
| Lendmodel | `lendmodel` | 4 | Cleanest sheet: USPTO zero (with a working control), .ai free, .app free, all registries free; .com $2,995 | "lend" is owned by finance; "lending model" is a credit-risk term; SEO lands in fintech |
| Model Remote | `modelremote` | 4 | Everything free incl. .com | "remote model" is a term of art for the opposite (Ollama cloud, ML Kit RemoteModel) |
| Sharemodel | `sharemodel` | 5 | registries free; **github.com/sharemodel is a squatted org** | "share a model" means publish the weights (Hugging Face, `ollama push`) — wrong about the one fact that defines the product |

## Rejected, with the collision

Modelshare (modelshare.ai, since 2022) · Sharellm (one letter from SharedLLM, a live self-hostable
per-user-key product) · Model Pass (modelpass.ai, a live OpenAI-compatible gateway with per-key quotas)
· Myllm (shipping iOS app + host binary) · Model Line (modelline.dev, private preview, E2EE model
calls) · Local Pass (`pass` = the Unix password store) · Model Seat (seat = per-seat licensing, i.e.
accounts) · Modelport (Archicad add-on) · Modelhost / Hostpass (reads as web hosting) · Localshare /
Localkey (LAN file transfer) · Guestkey (says nothing about AI).

## What round 2 established

- The audience's frame is **"my model", "access", "remote", "key"** — not hospitality. Every
  legibility-5 name is a `Model ___` compound; the noun after it is the whole decision.
- **Sharing-the-weights collision is structural:** any name with "share" in it reads as uploading
  weights to this audience. Out.
- **Under-description is the acceptable failure mode**; misdirection is not. Model Key's misread ("a
  keyring for my API keys") is a subset of the product; Model Access's ("Bedrock IAM") and Model
  Remote's ("cloud model") are its opposite.
- No trademark clearance was performed on any name; the MODELKEY Class 28 finding rests on one
  TMview session with positive controls. The holder of modelkey.ai/.dev is the biggest unknown.

## PM recommendation (round 2)

**Model Key** (`modelkey`), tagline carrying what the name omits: "Give friends a key to the model on
your machine." Fallbacks: Model Access if a free .ai outranks the Bedrock collision; Lendmodel if the
cleanest legal sheet outranks a legibility point. Before the post: a knock-out trademark search in
Classes 9/42 (US + EU) on the chosen name; a decision on modelkey.app vs. buying .com.

---

# Status 2026-09-03 (founder): no name yet; two rounds rejected

Round 1 (hospitality metaphors) described a feeling, not the product. Round 2 (audience vocabulary)
described the product and was generic. A third round was declined. Founder's diagnosis of the PM's
pattern: compounding two physical or descriptive words is not brandable — every result reads generic.
The brandable names in this space are invented or repurposed single words (Ollama, Tailscale, ngrok).

The social construct the product models (PM, 2026-09-03): one person's own machine working for their
circle — capacity shared under the owner's control, out of goodwill and competence, not for money.
Parallels: the Wi-Fi code (guest, code, use while here, owner can change it — the invite ritual); the
neighbor with the well; the household with the dish; the ham operator relaying calls under a call sign
(closest to the mechanism: a call sign, a rig); the home-lab operator running a service friends "get on";
patronage. Not a marketplace, co-op, mesh, or utility: the resource stays personal, access is granted.

**Founder is sleeping on it; decision expected 2026-09-04.** Until then: no naming rounds. When the
founder names it, the rename is ~1 hour (product constants in Go and TS, invite prefix, binary, docs),
then the F3 domain follows.
