## ADDED Requirements

### Requirement: More-articles copy output is safe at paste time
The WeChat official-account copy layer SHALL output the more-articles section so that cards survive when pasted into the WeChat editor and saved immediately, before asynchronous external-image transfer completes.

#### Scenario: Immediate draft save keeps populated cards
- **WHEN** a copied article contains populated `/ 更多文章 /` cards
- **WHEN** the pasted HTML is saved as a WeChat draft immediately after paste
- **THEN** every populated safe card MUST remain present as a `linktype="image"` card
- **THEN** the section MUST NOT disappear and later recover as a dependency on asynchronous transfer

#### Scenario: Empty placeholder survives immediate draft save
- **WHEN** a more-articles card has no article URL or cover yet
- **WHEN** the pasted HTML is saved immediately as a WeChat draft
- **THEN** its placeholder image MUST be a bitmap image that survives and can be replaced inside the WeChat editor

### Requirement: Link-card references satisfy T0 safety predicate
For WeChat official-account copy output, each populated more-articles `linktype="image"` card SHALL satisfy a T0 safety predicate before it is written to the clipboard.

#### Scenario: Card imgurl is WeChat hosted
- **WHEN** a populated more-articles card is rendered for official-account copy
- **THEN** the card `imgurl` attribute MUST be a WeChat image host URL when emitted as `linktype="image"`
- **THEN** the card `imgurl` MUST NOT be an OSS URL, external non-WeChat URL, `data:` URL, `blob:` URL, localhost URL, or relative URL

#### Scenario: Card src is immediately survivable
- **WHEN** a populated more-articles card is rendered for official-account copy
- **THEN** its `<img src>` MUST be either a WeChat-hosted image URL or a bitmap `data:image/png|jpeg|jpg|gif|webp` URL
- **THEN** an OSS or external HTTP image URL MUST NOT be the only representation required for immediate draft survival

#### Scenario: Non-WeChat cover does not become fake-safe link card
- **WHEN** no WeChat-hosted `imgurl` is available for a populated more-articles card
- **THEN** the copy layer MUST NOT emit a `linktype="image"` card that pretends to be draft-safe
- **THEN** the card MUST be safely downgraded or omitted according to implementation UI constraints

### Requirement: Background preparation only moves between safe states
Background jobs MAY improve later copy output, but they SHALL NOT write unsafe intermediate HTML into the current official-account copy layer.

#### Scenario: Copy click does not await preparation jobs
- **WHEN** the user clicks copy while more-articles background preparation is still running
- **THEN** the copy action MUST complete using the latest already-safe copy layer
- **THEN** the copy action MUST NOT await composite generation, OSS upload, or image transfer work before writing to the clipboard

#### Scenario: Background completion cannot write unsafe external src
- **WHEN** a more-articles background job finishes with an OSS or external HTTP composite URL
- **THEN** it MUST NOT write that URL as the only card `<img src>` in official-account copy output
- **THEN** it MAY update preview/cache output or later copy output only if the T0 safety predicate remains satisfied

#### Scenario: Stale jobs cannot overwrite newer copy generation
- **WHEN** a background job resolves after a newer article session or footer copy generation exists
- **THEN** it MUST NOT overwrite the newer `_footerCopy`

### Requirement: Draft-survival simulator models immediate and delayed save timing
The automated WeChat draft-survival tests SHALL model both immediate save and delayed save timing for pasted HTML.

#### Scenario: Immediate save does not grant external HTTP transfer
- **WHEN** the simulator runs in immediate-save mode
- **THEN** it MUST NOT assume external HTTP `<img src>` values have already been transferred to WeChat image hosts
- **THEN** output that only survives after delayed transfer MUST fail the immediate-save test

#### Scenario: Delayed save still preserves imgurl behavior
- **WHEN** the simulator runs in delayed-save mode
- **THEN** external HTTP `<img src>` values MAY be treated as transferred
- **THEN** `imgurl` attributes MUST still remain unchanged and non-WeChat `imgurl` values MUST still cause their `linktype="image"` cards to be removed
