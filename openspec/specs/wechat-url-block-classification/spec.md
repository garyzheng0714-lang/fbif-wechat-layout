## Purpose

Define a URL-import classifier that fixes body-as-caption failures without breaking caption forms that historical WeChat articles already rely on.

## Requirements

### Requirement: URL classification uses observation, article profile, and one role-decision stage

The classifier SHALL separate DOM observation, article-local profiling, and final role decisions before emitting public blocks.

#### Scenario: Observed text is role-neutral

- **WHEN** WeChat HTML is parsed
- **THEN** the walker MUST collect ordered facts such as text, style, DOM identity, media identity, and explicit relationships
- **THEN** ordinary observed text MUST NOT be emitted as final `h` or `cap` by the walker

#### Scenario: Article profile is request-local

- **WHEN** observed blocks are available
- **THEN** body flow, body typography, reference sections, and marker-heading candidates MUST be derived only from that article
- **THEN** no classification state may leak across imports

#### Scenario: Public block contract is unchanged

- **WHEN** roles are resolved
- **THEN** they MUST be converted to the existing public `Block` JSON shape
- **THEN** renderers MUST NOT require a new block contract

### Requirement: Every caption has a bounded association with one media item

The classifier SHALL support both explicit relationships and historically supported WeChat caption forms. Every emitted `cap` MUST carry a media ID, a scope ID, and an association proof.

The safety invariant is:

```text
role(text) == cap  =>  boundedMediaAssociation(text, media, scope, proof)
```

#### Scenario: Figure caption binds to exactly one media

- **WHEN** text is inside a `<figcaption>` whose nearest `<figure>` contains exactly one supported media node
- **THEN** it MUST be associated with that media
- **THEN** the decision MUST expose `figcaption` proof

#### Scenario: ARIA IDREF binds to one unique target

- **WHEN** exactly one media points through `aria-describedby` to an element ID occurring exactly once
- **THEN** text within that target MUST be associated with that media
- **THEN** the decision MUST expose `aria-describedby` proof

#### Scenario: Historical visual caption remains compatible

- **WHEN** text immediately follows one concrete media item and uses a previously supported caption signal such as muted/small typography, short centered layout, caption metadata, image-source wording, or same-paragraph image description
- **THEN** it MAY be associated with that media as a bounded legacy caption
- **THEN** it MUST expose a legacy-association proof and MUST remain subject to body-style barriers and length limits

#### Scenario: Existing correct caption fixtures remain unchanged

- **WHEN** the historical grey, small-font, centered-short, image-source, same-paragraph, long-caption, wide-image, and marker-boundary fixtures are replayed
- **THEN** every caption previously expected as `cap` MUST still be `cap`
- **THEN** their later heading and body roles MUST remain unchanged

#### Scenario: Ambiguous explicit declarations do not create an explicit proof

- **WHEN** a figure contains multiple media nodes, multiple media share one IDREF target, or an ID is duplicated
- **THEN** no explicit caption proof may be created from that declaration
- **THEN** the text may only follow the separately bounded compatibility path if it independently satisfies that path

### Requirement: Legacy compatibility is calibrated against the article's own body

Absolute values such as 14px or `#555` SHALL NOT be treated as universally caption-like. The classifier SHALL learn the typography used by obvious prose elsewhere in the same article and use it as negative evidence.

#### Scenario: Candidate matches article body typography

- **WHEN** text after an image has the same effective font size, color, and alignment as established prose in that article
- **THEN** visual caption signals MUST NOT classify it as `cap`
- **THEN** it MUST continue through the normal body/heading decision process

#### Scenario: Real caption differs from article body

- **WHEN** a historical visual caption is associated with an image and its typography differs from the article's established body typography
- **THEN** the compatibility path MUST continue to recognize it as `cap`

#### Scenario: Long caption does not contaminate body profiling

- **WHEN** a long styled caption immediately follows a media item
- **THEN** that first post-media block MUST NOT define the article's body typography merely because it is long or sentence-like

### Requirement: Caption association is media-local and cannot leak

Caption state SHALL be keyed by media ID and scope. It SHALL NOT be represented by an unbound global boolean.

#### Scenario: Explicit association ends at explicit scope

- **WHEN** a proven caption is followed by text outside its `<figcaption>` or IDREF target
- **THEN** later text MUST NOT inherit the caption role

#### Scenario: Legacy association stops at body typography

- **WHEN** a compatible legacy caption is followed by text matching the article's body typography
- **THEN** the following text MUST remain `txt`
- **THEN** later paragraphs MUST NOT inherit caption state

#### Scenario: Image source terminates a legacy caption group

- **WHEN** an image-source line is included in a legacy caption group
- **THEN** that line MUST remain `cap`
- **THEN** the following paragraph MUST begin normal classification rather than inheriting the group

#### Scenario: Association cannot cross media

- **WHEN** another media block is encountered
- **THEN** any prior caption group MUST end
- **THEN** the new media MUST receive an independent scope and length budget

### Requirement: Caption character limits apply to explicit and compatible captions

Configured limits SHALL be final safety valves after media association.

#### Scenario: Single caption block exceeds its limit

- **WHEN** an associated caption block exceeds its applicable base or muted/small-style limit
- **THEN** it MUST be emitted as `txt`
- **THEN** source wording, class metadata, or explicit markup MUST NOT bypass the limit

#### Scenario: Caption scope exceeds its cumulative limit

- **WHEN** associated blocks in one media/scope cumulatively exceed the selected limit
- **THEN** the overflowing block and all later blocks in that scope MUST be emitted as `txt`
- **THEN** other media scopes MUST remain independent

### Requirement: Images may still form section-marker heading groups

Caption fixes SHALL NOT change the existing article-local marker-heading relationship.

#### Scenario: Decorative marker with heading text forms a section header

- **WHEN** a decorative or numbered marker image is followed by isolated heading text and body flow
- **THEN** the heading text MUST be emitted as `h`
- **THEN** it MUST NOT be consumed by caption compatibility

#### Scenario: Dimensionless unknown media does not swallow plain text

- **WHEN** media has no dimensions and the following short text has no caption signal
- **THEN** adjacency alone MUST NOT emit `cap`

### Requirement: Reported URL and historical positives are dual regression gates

Fixes SHALL pass both the reported false-positive fixtures and all historical true-positive caption fixtures. Passing only one side is insufficient.

#### Scenario: Reported header and body remain text

- **WHEN** the minimal fixture derived from `https://mp.weixin.qq.com/s/LmvhTcF2V7WVfY7xJTAhmQ` is parsed
- **THEN** author/copyright metadata and the following question/prose MUST remain `txt`
- **THEN** removing the metadata so the question directly follows the banner MUST NOT change that prose into `cap`

#### Scenario: Reported source caption and later prose coexist

- **WHEN** the historical visual source caption `图源：Neuro` follows its image and later prose uses the article's 14px dark-grey body typography
- **THEN** the source line MUST remain `cap`
- **THEN** every later prose paragraph MUST remain `txt`

#### Scenario: Compatibility change cannot rewrite old positives

- **WHEN** any caption-classification implementation changes
- **THEN** the original historical caption fixtures MUST be replayed without replacing them with explicit markup
- **THEN** `go test ./...` and `npm test` MUST pass

### Requirement: Guarantee boundary is explicit

The classifier SHALL guarantee bounded media association, body-style isolation, scope isolation, and regression compatibility. It SHALL NOT claim perfect recovery of author intent from arbitrary HTML.

#### Scenario: Source provides no distinguishing information

- **WHEN** body and caption are indistinguishable in relationship, typography, wording, and surrounding flow
- **THEN** deterministic classification may remain ambiguous
- **THEN** the product MUST prefer readable output and MUST NOT allow an inferred caption group to propagate without a media-bound scope
