# sharedwatch — image generation prompts

Fifteen image prompts for branding and marketing assets (GitHub social card, repo banner, landing-page hero, section illustrations, OG image, mobile tiles). Written for a human artist or for a capable image-generation model that takes natural-language briefs (Midjourney, DALL-E 3, Stable Diffusion XL, Adobe Firefly).

A separate shape-checker will crop/pad images that arrive at the wrong aspect ratio. Each prompt declares its **ideal aspect ratio** so the composition is designed for that frame from the start; the checker only fixes drift, not the artistic intent.

---

## Brand foundation (read before writing any image)

**The product, in one sentence.** sharedwatch is a calm, durable, pull-based activity journal for a local shared folder. It watches without interrupting; the reader pulls digests on their own cadence. Single binary. Local. Honest.

**The audience.** Developers, sysadmins, AI-agent operators. People who reach for tools that don't shout. They notice the quality of a hand-stitched wallet and the weight of a well-balanced wrench. They distrust glossy marketing.

**The feeling we're chasing.** Quiet competence. The vibe of a watchmaker's bench, a lighthouse at dawn, a leather-bound field journal, a well-loved brass compass. Trustworthy without being heavy. Modern without being chrome. Patient.

**The feelings we're NOT chasing.** Urgency. Hype. Hustle. Surveillance-state coldness. SaaS-pastel cheerfulness. AI-uncanny-valley. Cyberpunk. Gradient meshes. "Hero in a hoodie staring at code."

### Visual system (use consistently across all 15)

**Palette** (give the artist these or close equivalents):
- **Stone slate** `#2A3540` → `#4A5566` (primary darks)
- **Aged cream** `#F4EAD5` → `#D9C89A` (paper, fabric, light)
- **Brass / amber** `#B48C4A` (single warm accent — never garish)
- **Moss shadow** `#3A4A35` (deep neutral, for foliage / cloth)
- **Ink** `#1B1F22` (the deepest darks; never pure black)

**Light.** Always natural or warm-incandescent. Single dominant source: dawn through a window, a brass desk lamp, an overcast skylight, golden hour through pine trees. Soft falloff. Never fluorescent, never RGB.

**Texture.** Visible. Paper grain. Worn leather. Cold-rolled brass with patina. Linen weave. Cracked plaster. Bonsai bark. Never glossy plastic. Never digital chrome.

**Composition.** One clear subject per frame. Generous negative space (room for headlines, taglines, app chrome on top later). Rule of thirds. Symmetry where it earns it. Shallow depth of field on photographic prompts.

**Strict don'ts.**
- No human faces (use hands, silhouettes, backs of heads — keeps it inclusive and dodges stock-photo cliché).
- No on-screen text/logos in the image itself (text is added later in CSS/HTML, except prompt #15 which is explicitly a brand card).
- No modern computer screens. Vintage CRTs, mechanical typewriters, paper, or analog dials are fine.
- No floating UI elements, no "dashboard mockups", no SaaS skeuomorphism.
- No anthropomorphic robots, no glowing eyes, no "AI brain" clichés.
- No people in hoodies hunched over keyboards.

**Style references to give the artist.** Kinfolk magazine. Cereal magazine. Stripe Press book covers. Aesop apothecary. Penguin Classics paperback covers (1960s). The films of Wes Anderson (framing) and Andrei Tarkovsky (patience). Photography of Wolfgang Tillmans (light) and Joel Meyerowitz (color).

### Aspect-ratio glossary

| Use | Ratio | Where it goes |
|---|---|---|
| **Hero landscape** | 16:9 | Landing-page hero, blog cover, YouTube thumbnail |
| **Wide banner** | 3:1 | GitHub README header, social cover photo |
| **Square** | 1:1 | App icon, profile pic, social post, GitHub social card |
| **Open graph** | 1.91:1 | OG/Twitter card link preview |
| **Portrait** | 3:4 | Mobile hero, Instagram feed, tall sidebar |
| **Editorial** | 3:2 | Section illustration, blog inline, gallery |

The checker will reshape, but composition matters: a subject placed dead-centre survives any crop; a subject placed at the edge does not.

---

## 1. HERO — *Lighthouse at dawn over a quiet sea*

**Use:** Landing-page hero, the single most important image. The soul of the brand.
**Aspect ratio:** 16:9 landscape.
**Mood in one sentence:** The thing you trust to stay on through the night, still glowing softly as the day begins.

**Background layer.** A pale, milk-grey dawn sky filling the upper two-thirds of the frame. A horizon line about one-third up from the bottom, slightly hazy. Just the suggestion of low clouds catching the first warm light from the east.

**Middle layer.** A calm, gently-textured sea taking the bottom third. Long slow swells, no breakers. The water reflects the dawn in muted slate-and-amber bands. A single small wooden fishing boat moored far in the distance — barely visible — gives scale and tells us the lighthouse is *for* something.

**Subject layer.** An old stone lighthouse on a low rocky outcrop, set in the right-hand third of the frame (rule of thirds). Granite-grey stones, weathered but maintained. The lantern room at the top glows with a steady warm amber light — not a sweeping beam, not a flash, just *on*. The light is honest, not theatrical.

**Foreground layer.** Wet dark rocks at the base of the lighthouse, foam from the gentle sea. A single seabird (suggested, not detailed) standing on a rock.

**Lighting.** Soft, low, from the right (rising sun, off-frame). Cool ambient blue-grey from the sky; warm amber pool from the lantern room and a slight kiss of warm rim-light on the right side of the lighthouse stones.

**Palette.** Stone slate, aged cream in the sky, single warm amber accent from the lantern, deep moss shadow in the rocks.

**Style references.** Photography of Joel Meyerowitz's Cape Light series. Painted minimalism in the vein of Edward Hopper's Lighthouse at Two Lights, but softer.

**Avoid.** Dramatic storm clouds. A beam of light slicing through fog (too clichéd). Lens flares. Saturated colours. Any visible text or branding on the lighthouse.

**Why this works.** The lighthouse *is* sharedwatch — present, durable, on. The boat is the user; the sea is the work; the dawn is the calm cadence we promise. Negative space in the upper-left holds the wordmark and tagline.

---

## 2. REPO BANNER — *A field journal and brass compass on a weathered desk*

**Use:** GitHub README header, social cover photo, blog series banner.
**Aspect ratio:** 3:1 ultra-wide.
**Mood in one sentence:** The desk of someone who keeps careful, lasting records by hand.

**Background layer.** A weathered dark-oak desktop fills the frame from edge to edge. Slight grain visible. Subtle ring-marks from old coffee cups. Soft warm side-light skimming across it.

**Middle layer.** Items arranged left-to-right with breathing room between them:
- (far left) An open leather-bound field notebook, page filled with neat handwritten observations in fountain-pen ink — dated entries, columnar layout. Some entries clearly in different hands (different inks, different slants).
- (centre-left) A brass compass, open, glass face catching the light.
- (centre) An old folded canvas map, partially unfolded, showing watch-points marked with tiny circles.
- (centre-right) A magnifying glass laid flat across the map's corner.
- (right) A single fountain pen with brass barrel, cap off, resting on a folded linen cloth.

**Subject layer.** No single hero — the *arrangement itself* is the subject. The composition reads left-to-right like a sentence: record, orient, map, examine, write.

**Foreground layer.** A few tiny details: a pressed leaf in the notebook gutter, a sprinkle of pencil shavings near the pen, the warm shadow of an unseen lamp.

**Lighting.** Single warm desk lamp from the upper right, soft falloff into the left edge. Just enough shadow to make the brass and glass elements catch and sparkle without being theatrical.

**Palette.** Stone slate desk; aged cream of the paper map and notebook page; brass accent from the compass, pen, and magnifier; moss-shadow at the edges.

**Style references.** Kinfolk magazine flat-lays, but with more atmosphere. The opening title-card aesthetic of Wes Anderson's *Grand Budapest Hotel*.

**Avoid.** Modern objects of any kind. A laptop. Headphones. A coffee cup with a chain-store logo. A keyboard. Anything that breaks the analog spell.

**Why this works.** Tells the whole story in one read: careful observation, multiple writers (different hands in the journal), durable instruments, intentional craft. The wide format makes it ideal for cropping with a wordmark on the right.

---

## 3. SQUARE ICON — *A single watchful eye, hand-drawn in a notebook margin*

**Use:** App icon (favicon, PWA, system tray), GitHub social card, profile picture.
**Aspect ratio:** 1:1 square.
**Mood in one sentence:** A friendly, patient witness — not a guard, not a camera.

**Background layer.** A textured cream paper page filling the entire frame. Fine paper grain visible. A few faint horizontal rule lines, very pale. One small ink smudge in the upper-left corner — character, not damage.

**Middle layer.** Nothing — the eye is on the paper directly.

**Subject layer.** A single eye, drawn in confident fountain-pen line work, perfectly centred in the frame. Not photorealistic — illustrated in the manner of botanical or scientific field drawings. Almond-shaped, calm gaze looking softly forward (not staring). The iris detailed with fine concentric line work like a watchmaker's loupe or a tree's growth rings. The pupil is a single solid amber-brass dot — the only colour in the image.

**Foreground layer.** A single small handwritten note in the lower right, in the same ink: a date — `2026·05·24` — written like a field-journal entry.

**Lighting.** Flat, even — as if photographed straight-on under soft window light. No dramatic shadow.

**Palette.** Cream paper, ink-black line work, single brass accent on the pupil. Two colours total.

**Style references.** Botanical illustrations from Maria Sibylla Merian. Saul Steinberg line drawings. Penguin Classics title-page emblems.

**Avoid.** Anything that looks like a security-camera eye, a robot eye, a glowing AI eye, or the Eye of Sauron. Avoid coloured iris textures, eyelashes, makeup. Avoid any photorealistic skin tones.

**Why this works.** A symbol that reads at 16×16 (favicon) and 1024×1024 (app store) equally well. The eye says "I'm watching, with care" — exactly the product's promise. The single amber dot threads it back to the brand palette.

---

## 4. CALM — *A single bonsai under skylight*

**Use:** Landing-page section illustration ("calm" or "no interruptions"), blog inline, social tile.
**Aspect ratio:** 3:2 editorial landscape.
**Mood in one sentence:** Patient attention to one thing, for a long time.

**Background layer.** A blurred-out workshop interior — soft grey plaster wall, suggestion of a wooden shelving unit on the far edge of the frame. Out of focus.

**Middle layer.** A low, dark-wood pedestal table. The wood has visible grain and the soft warmth of decades of use.

**Subject layer.** A single mature bonsai pine, no taller than a forearm, in a hand-thrown stoneware pot the colour of wet slate. The needles are crisp, the trunk gnarled. The composition of the tree itself follows classical bonsai sanzon (asymmetric triangle).

**Foreground layer.** Slight blur on a few floating dust motes catching the light, just in front of the tree.

**Lighting.** A single shaft of cool daylight from a skylight or high window above and slightly behind, illuminating the top of the tree and the table edge. The pot sits in soft shadow. Dust motes are visible in the beam.

**Palette.** Moss-shadow needles, stone-slate pot, aged-cream highlight on the dust-motes, brass-warm wood of the table.

**Style references.** Photography of Yoshikazu Shirakawa. Stillness of Tarkovsky interiors.

**Avoid.** A second tree. A garden. A person tending it. Anything that breaks the singularity of the focal subject.

**Why this works.** Bonsai *is* attentive observation distilled into ritual. It mirrors the watcher's daily passive cadence — small, regular, patient. The skylight light evokes "ambient" without ever saying it.

---

## 5. DURABILITY — *A stack of leather-bound ledgers on a dark wooden shelf*

**Use:** Section illustration for "durable journal" / "restart-safe" / "SQLite-backed", changelog page header.
**Aspect ratio:** 3:2 editorial landscape (works as 1:1 square also).
**Mood in one sentence:** Years of careful record-keeping, every entry still findable.

**Background layer.** A dark-stained oak shelf interior, deep brown-black. A second row of book spines visible on a shelf above, slightly blurred.

**Middle layer.** A solid stack of seven or eight large leather-bound ledger books, oxblood and forest-green and walnut spines, gilt year-labels on the spines (illegible at distance — just suggestive). The stack is slightly off-axis, lived-in.

**Subject layer.** The topmost ledger lies open across the top of the stack, revealing a page of careful columnar handwriting: dates down the left in a confident hand, entries in three columns. The page is the focal point — the only fully sharp element.

**Foreground layer.** A brass bookmark ribbon hangs out of the closed book second from the top. A single fountain pen rests on the open page, cap off, brass nib catching the light.

**Lighting.** Warm side-light from a window or lamp out of frame to the right. Strong but soft, raking across the spines and the open page.

**Palette.** Dark stone-slate and moss-shadow leather, aged cream open page, brass accents from the bookmark, the pen, and the lighting.

**Style references.** The Beinecke Rare Book Library at Yale. Photography from the *Slow Reading* movement.

**Avoid.** Tied scrolls. Wax seals. Anything that pushes too far toward "fantasy library" — we want serious craft, not Hogwarts.

**Why this works.** Says "your data lasts, decade after decade" without ever using the word "durable". The visible different spine colours hint at versioned releases.

---

## 6. MULTI-ACTOR COORDINATION — *Three workbenches sharing a long central tool table*

**Use:** Section illustration for multi-root / multi-actor coordination, blog post on collaboration.
**Aspect ratio:** 3:2 editorial landscape.
**Mood in one sentence:** Independent craftspeople, shared infrastructure, no one in the way.

**Background layer.** A bright but soft-lit workshop interior. Whitewashed brick wall, exposed beams above, large skylight visible at the top of the frame.

**Middle layer.** Three solid wooden workbenches arranged at slight angles to each other, like points of a loose triangle. Each bench has the in-progress work of one craft — leather-stitching tools on the left bench, an open book and ink on the middle one, brass clock-making parts laid out on the right. Each bench is *personal*. None encroaches on another.

**Subject layer.** A long, narrow, dark-wood **central table** between them, holding the shared tools: a tape measure, a magnifying lamp, three different sizes of hammer, a shared lamp, a kettle on a small spirit-burner. This central table is the load-bearing visual element — the *shared substrate*.

**Foreground layer.** Subtle out-of-focus elements at the bottom — the corner of a chair back, a coil of leather strap.

**Lighting.** Cool top-light from the skylight, with warm pools from a single lamp on each bench and the shared central lamp. Light feels democratic — no one bench is privileged.

**Palette.** Aged cream walls, stone-slate benches, brass on tools and lamps, moss-shadow shadows in corners.

**Style references.** *The Workshops of the World* photography series. Cereal magazine atelier features.

**Avoid.** Three identical setups (boring). Any one bench being dominant. A foreman/supervisor figure. A computer.

**Why this works.** Visualises the actual architectural promise: independent watchers, one shared journal between them, no one blocks anyone. The triangular arrangement reads as "peers", not hierarchy.

---

## 7. PULL VS PUSH — *An old mechanical tide gauge on a harbor wall at low tide*

**Use:** Section illustration for "pull-based, not push", blog explainer on cadence.
**Aspect ratio:** 4:3 (portrait-ish landscape).
**Mood in one sentence:** Information that waits patiently for you to come look.

**Background layer.** Calm, low-tide harbor water in the lower third. Stone harbor wall rises through the rest of the frame, pitted with age and crusted with the white lines of past tide levels. Beyond the wall, blurred sailing masts at distance.

**Middle layer.** The harbor wall takes most of the frame, full of texture — barnacles, lichen, hand-cut stone.

**Subject layer.** A cast-iron mechanical tide-gauge mounted to the wall at chest height. Brass face, single needle, calibrated dial in elegant serif numerals. Patinated but maintained. The needle reads "low" — it's just sitting, available, no one watching it, doing its job.

**Foreground layer.** A small carved stone bench at the wall's base, empty. An invitation to come and look.

**Lighting.** Overcast cool daylight, gentle and even. A small warm pool on the brass face of the gauge, as if catching a break in the clouds.

**Palette.** Stone-slate wall, sea-grey water, brass accent on the gauge, moss-shadow in the wet crevices, single hint of warm cream where the sun breaks through.

**Style references.** Photography of coastal Northumberland or Cornwall. Sebastião Salgado's quiet documentary palette.

**Avoid.** Anyone using the gauge. A digital display. Modern equipment. Tourists.

**Why this works.** A tide gauge is the pull-based metaphor incarnate: it measures continuously, displays passively, and serves whoever happens to walk up to it. The empty bench is the user's seat — waiting for them.

---

## 8. AMBIENT AWARENESS — *A flap-board departure indicator in a near-empty Mediterranean train station at dusk*

**Use:** Section illustration for "ambient awareness, no interruption", landing-page sub-hero.
**Aspect ratio:** 16:9 wide landscape.
**Mood in one sentence:** Updates happening calmly in the background while you read your paper.

**Background layer.** A high vaulted train-station interior at dusk. Iron arches, dusk light slanting in horizontally through high arched windows. Polished marble floor with subtle reflection.

**Middle layer.** A single solovely vintage Solari flap-board departure indicator mounted on the wall, rows of letters and numbers visible. Mid-flip on one row — a few letters caught in motion, blurred slightly. It's *changing*, calmly, with that faint mechanical clatter you can almost hear in the image.

**Subject layer.** A single person seen from behind, sitting on a long curved wooden bench, reading a folded newspaper. Their attention is on the paper, not on the board. They're aware the board is updating — they just don't care, this minute.

**Foreground layer.** The end of the bench, slightly out of focus, with a small leather satchel set on the floor beside it. A subtle warm pool of light from a hanging Edison-bulb fixture above the bench.

**Lighting.** Cool slate-blue from the dusk windows, warm amber pools from the hanging bulb. High contrast but soft edges.

**Palette.** Stone slate (marble floor, iron arches), aged cream (the newspaper, the marble highlights), brass amber (hanging bulb, board frame), moss shadow (the person's coat).

**Style references.** Wes Anderson's *The Grand Budapest Hotel*. Photographs of Gare du Nord by Henri Cartier-Bresson.

**Avoid.** A rushed traveller. A digital screen anywhere. A face. A modern electronic clock.

**Why this works.** The whole point of sharedwatch is: the journal updates calmly whether you're watching or not, and you go to it when you want to. The reader on the bench is the user — present, aware, unhurried.

---

## 9. YOUR CONTROL — *A craftsperson's hand turning a single brass dial*

**Use:** Section illustration for "configuration is yours", small grid tile, social post.
**Aspect ratio:** 4:5 portrait (also crops well to 1:1).
**Mood in one sentence:** One deliberate motion, a clear control, a real tool.

**Background layer.** Soft-focus dark workshop background — suggestion of pegboard with tools hung in shadow.

**Middle layer.** Aged walnut workbench surface filling the frame's lower half. Visible grain, deep with patina.

**Subject layer.** A single human hand — adult, no rings, no watch, weathered but clean — turning a single large brass control dial mounted on a small piece of equipment. The dial is the size of a hockey puck, knurled grip, with a single indicator mark and a small engraved scale visible. The hand grips it with thumb and two fingers, mid-turn, calm and certain.

**Foreground layer.** Slight blur on the edge of the bench. A coiled cable runs out of frame to the left from the equipment.

**Lighting.** Single warm desk-lamp light from upper-left, raking across the brass and catching the back of the hand. Deep shadow behind. Cinematic but not theatrical.

**Palette.** Walnut bench in dark stone-slate, the hand in aged-cream skin tones, the brass dial as the burning amber focal point, ink-black shadow behind.

**Style references.** Hand-tool photography from the *Lost Art Press* publishing house. Old Leica brochure stills.

**Avoid.** A face. A wristwatch (clichéd watchmaker imagery — we want a different connotation). Fingernails painted any colour. Multiple hands.

**Why this works.** Sells **agency**. The user is in control; the dial is concrete and physical; there is no abstraction or magic.

---

## 10. THE JOURNAL — *An open shared notebook with multiple handwritings*

**Use:** Documentation header, "what gets stored" explainer, blog post header on data shape.
**Aspect ratio:** 3:2 editorial landscape.
**Mood in one sentence:** One notebook, many hands, every entry timestamped, no one's writing erased.

**Background layer.** A dark-stained desk surface, lightly visible at the edges.

**Middle layer.** Just the desk — the notebook is huge in the frame.

**Subject layer.** A large cloth-bound field journal lies open, taking 80% of the frame. Two facing pages visible. Both pages are filled with handwritten entries in clearly *different hands*: some in confident upright fountain-pen ink, some in a smaller mechanical-pencil scrawl, some in elegant slanted cursive. Each entry begins with a timestamp (`08:14`, `08:22`, `09:01`...). Some entries are short, some are paragraphs. Some have small marginalia: a circled note, an arrow drawn between two entries on opposite pages (a "reference"), a single tiny illustration of a leaf.

**Foreground layer.** A pressed dried leaf sits in the gutter between the pages. A brass paperweight pins the upper-left corner. A fountain pen rests at the bottom-right, uncapped, ink visible.

**Lighting.** Warm overhead diffuse light, very even. The kind of light you'd want for sustained reading. No dramatic shadow.

**Palette.** Stone-slate desk; aged-cream pages dominate; brass paperweight + pen as warm anchor; moss-shadow leaf; ink-black handwriting in different shades.

**Style references.** Charles Darwin's *Beagle* notebooks. The Field Museum's botanical journal archive.

**Avoid.** Computer text. Printed labels. A page where every entry is in the same handwriting (defeats the point). Anything that looks like a personal diary.

**Why this works.** Visualises `events.payload_json` with `actor` field: multiple writers, one durable shared document, every entry attributed and timestamped, references between entries (the arrow = `ref_event_id`).

---

## 11. DEVELOPER AT WORK — *A clean wooden desk, one screen, one cup of coffee, late afternoon*

**Use:** "For developers" section, About page, product photography for press.
**Aspect ratio:** 3:2 editorial landscape.
**Mood in one sentence:** A real human's deliberate workday — not a Hollywood hacker scene.

**Background layer.** A serene home-office interior. Pale linen curtain on the left, soft daylight filtered through it. A small framed print on the wall, abstract, illegible from this distance.

**Middle layer.** A medium-toned oak desk, uncluttered. A leather desk-pad covering the centre.

**Subject layer.** A small footprint of essentials: an open ceramic-cream notebook with a pen across it; a single ceramic mug with steam; a small succulent in a stoneware pot; and the *back* of a small modern aluminum laptop, lid half-open, screen out of view (this is critical — we never see the screen).

**Foreground layer.** A pair of hands resting calmly on either side of the notebook, mid-action. Plain plain dark-grey sleeves of a soft sweater visible at the wrists. No rings, no watch, no specific identifying details.

**Lighting.** Late-afternoon side-light from the linen-curtained window on the left. Warm, soft, painterly. A small warm desk-lamp on the upper-right adds a gentle counter-light.

**Palette.** Aged-cream curtain and notebook, stone-slate desk and sweater, brass accent in the lamp and the steam-light, moss-shadow plant.

**Style references.** Photography of David Lebovitz's kitchen series (warm domesticity). Apartamento magazine portraits without the people.

**Avoid.** Multiple monitors. RGB anything. Mechanical keyboard close-ups. A messy desk. A coffee mug with a logo. A hoodie. Headphones.

**Why this works.** Counters every cliché about who uses dev tooling. This is competence at rest, not panic in motion.

---

## 12. ARCHIVE / RETRIEVAL — *A library card catalog drawer, partially open, hands flipping through cards*

**Use:** "Events query" section, SQL docs header, search/retrieval illustrations.
**Aspect ratio:** 3:2 editorial landscape (also works 1:1).
**Mood in one sentence:** Everything is filed. Everything is findable.

**Background layer.** A whole wall of identical wooden card-catalog drawers, fading into soft focus. Each drawer has a small brass handle and a tiny label-holder. The wall takes the upper two-thirds of the frame.

**Middle layer.** One specific drawer in the centre-foreground pulled fully out, sitting on a small wooden ledge. Hundreds of typewriter-typed index cards visible inside, edges yellowed.

**Subject layer.** Two adult hands gently lifting and flipping through cards toward the front of the drawer. The top visible card is a "file event" record: typed header `2026·05·24  08:14  file.modified`, with a typed body of observation notes below it. The card is in sharp focus.

**Foreground layer.** A few cards have been gently set aside on the wooden ledge to the left of the drawer.

**Lighting.** Warm overhead lamps reflecting off the wood, just enough specular highlight on the brass handles to give the wall a quiet rhythm. The drawer interior is well-lit, the cards readable.

**Palette.** Walnut stone-slate cabinetry, aged-cream cards, brass handles and label-holders, moss-shadow drawer interiors.

**Style references.** Old US Library of Congress photography. *The Catalog* documentary aesthetic.

**Avoid.** A modern computer search interface. Anyone looking at the camera. A digital sign anywhere.

**Why this works.** This *is* what `events list` does — pull the drawer of the time-window you want, flip to find what you're looking for, references between cards (`ref_event_id`). Card catalogs are the perfect historical metaphor for the queryable journal.

---

## 13. CADENCE — *A wall of workshop clocks, all slightly out of sync, all calm*

**Use:** "Modes" section (passive vs active), changelog header, "your cadence" page.
**Aspect ratio:** 3:2 editorial landscape (also works 1:1).
**Mood in one sentence:** Time, kept calmly, in many independent rhythms that don't have to agree.

**Background layer.** A whitewashed workshop wall, soft and slightly aged with subtle ochre stains.

**Middle layer.** Nothing — just wall.

**Subject layer.** Eight or nine wall clocks of varying styles mounted on the wall in a loose grid: a brass railway clock, a wooden cuckoo (closed), a marine chronometer, a simple round school clock, an antique pendulum clock, a small ship's bell-clock, a Bauhaus minimalist clock, a folk-art painted clock. Each shows a *slightly different time* — minutes off from each other, by design. None is alarming, all are calm. A few have visible pendulums, mid-swing, captured slightly blurred (suggesting motion, life).

**Foreground layer.** A single small bench below, with a brass winding key resting on it. Suggests the unseen caretaker.

**Lighting.** Soft overhead daylight from a high window out of frame above. Even, gentle. The brass elements catch warmly.

**Palette.** Aged-cream wall, stone-slate and walnut clock frames, brass accents, moss-shadow shadow under each clock.

**Style references.** Bernd & Hilla Becher's typology photography (the *grid* aesthetic). The clock collection rooms at the Greenwich Royal Observatory.

**Avoid.** Digital clocks. A central "master" clock larger than the rest. Anyone winding the clocks.

**Why this works.** Different cadences (passive 10m, active 5s, reconcile 30m) all running independently, all calm, no clock interrupting another. Bauhaus + folk + railway = the broad audience this product reaches.

---

## 14. COOPERATION — *Two pairs of hands working at the same workbench, one slightly more "elegant" than the other*

**Use:** Section illustration for human/AI cooperation, "multi-agent" docs header, the most explicit "AI agent" image we'll make.
**Aspect ratio:** 3:2 editorial landscape.
**Mood in one sentence:** Two careful workers, one shared piece of work, no fear and no hierarchy.

**Background layer.** A soft-focus workshop interior, warm and a bit dim.

**Middle layer.** A large oak workbench dominating the frame. On it, a single shared open notebook (echoing image #10), a shared brass lamp, a few tools laid out neutrally.

**Subject layer.** Two pairs of hands meeting in the centre over the notebook. Left pair: clearly **human** — slightly weathered skin, a tiny pencil-callus on the index finger, holding a pencil. Right pair: also human-shaped, but wearing **soft suede or fabric gloves** (light cream/oatmeal colour, not techy), holding a different tool (a brass stylus). The gloved hands are not robotic; they're just *differentiated* — perhaps a botanist's gloves, a conservator's gloves. The differentiation is subtle, kind, not jarring.

**Foreground layer.** The shared lamp's warm pool of light unifies both pairs of hands. A second cup of tea, in a slightly different style than the first, sits at the right edge.

**Lighting.** Single warm lamp from above, casting both pairs of hands into the same circle of light. Soft, intimate.

**Palette.** Stone-slate bench, aged-cream notebook and gloves, brass tools and lamp, moss-shadow sweater sleeves of both workers.

**Style references.** Conservation studio photography (the Tate, the Smithsonian). NPR Tiny Desk *aesthetic*, applied to crafts.

**Avoid.** A literal robot hand. Anything chrome. A glowing eye. Any visible "AI" branding. The two workers looking competitive. Multiple cups of identical coffee (it's deliberate that they're slightly different — they're individuals).

**Why this works.** Models the actual relationship: a human and an AI agent share the same journal, the same workspace, the same tools — but they're distinguished by attribution (`actor` field). Neither dominant, both careful. The gloves convey "different kind of worker" without "different species".

---

## 15. BRAND CARD / OG IMAGE — *Aged paper with a single subtle lighthouse silhouette and the wordmark*

**Use:** Open-graph / Twitter card link preview, social posts, presentation title slide.
**Aspect ratio:** 1.91:1 (standard OG / Twitter card; tolerates crop to 1:1).
**Mood in one sentence:** A printed label from a workshop that's been making good things for a long time.

**Background layer.** Full-frame aged cream paper texture, slightly mottled, the way a hundred-year-old book endpaper looks. Subtle deckled edges where it meets the frame.

**Middle layer.** Centred slightly above the visual middle: a clean, restrained serif wordmark — `sharedwatch` — in a single typographic weight (think Caslon, Lyon, or a contemporary serif like GT Sectra). All-lowercase. Ink-black. Letterforms confident, slightly bookish, never trendy. Below it, in a smaller italic of the same family: `calm collaboration bus for a local shared folder` — the tagline.

**Subject layer.** To the left of the wordmark, a small **silhouette of a lighthouse** drawn in single-line ink, no bigger than the cap-height of the wordmark. It's a quiet emblem, not a logo to itself — just an echo of image #1.

**Foreground layer.** In the lower right corner, a single small line: `v0.0.3 · MIT · github.com/ab0t-com/sharedwatch` in tiny monospace, the way a fine-press book lists its colophon.

**Lighting.** None — flat, printed-page rendering. As if scanned from a real letterpress page.

**Palette.** Aged cream paper, ink-black type, single brass-amber dot on the lantern of the silhouette (the only colour anywhere).

**Style references.** Stripe Press book covers. Penguin classics title pages. Cooper Hewitt design museum exhibit labels.

**Avoid.** Gradients. Drop shadows. Glow. Multiple fonts. Stock-photo backgrounds. Anything that says "Canva template".

**Why this works.** This is the card that shows when someone shares the URL. It needs to look unmistakeably like *this* project at a glance. The wordmark + lighthouse echo gives a brand system that can repeat across stickers, business cards, presentations.

---

## Notes for whoever runs these prompts

- **Run each prompt at its declared aspect ratio.** The reshape-checker will pad or crop, but always trust the artist's framing first.
- **Generate multiple variants per prompt** (4–8) and pick the one that feels most like the brand foundation above, not the most technically impressive.
- **Resist the urge to "improve" the palette.** If an image arrives with garish blues or oranges, regenerate; don't keep it.
- **No upscaling beyond 2×.** Honest grain is part of the look.
- **Save the final selections under** `docs/brand/assets/<prompt-number>-<short-name>.png` (or `.jpg` for photographic ones). Keep originals at full resolution; produce web-optimised derivatives separately.
- **Re-read the "strict don'ts" before approving any image.** That list is the brand boundary; everything inside is fine.
