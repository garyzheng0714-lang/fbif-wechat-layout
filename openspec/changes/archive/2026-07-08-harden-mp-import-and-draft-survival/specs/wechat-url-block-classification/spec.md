## ADDED Requirements

### Requirement: URL classification uses observation, profile, and role decisions
The URL block classifier SHALL separate raw observation, article-local profiling, and final role decisions before emitting public blocks.

#### Scenario: Observed blocks do not decide final role
- **WHEN** WeChat HTML is parsed from URL import
- **THEN** the classifier MUST first collect ordered observed blocks containing raw facts such as text, runs, tag, classes, style summary, image attributes, source markers, and neighboring structure
- **THEN** observed blocks MUST NOT directly decide whether a text is `h`, `txt`, or `cap`

#### Scenario: Article profile is local to the imported article
- **WHEN** observed blocks are available
- **THEN** the classifier MUST derive article-local context such as body flow candidates, repeated body style, caption-like subordinate text, and reference-section candidates
- **THEN** the classifier MUST NOT require a global fixed heading font-size threshold to decide all headings

#### Scenario: Role decisions produce public blocks
- **WHEN** role decisions have been resolved
- **THEN** the classifier MUST convert those decisions into the existing public `Block` JSON shape
- **THEN** frontend renderers MUST NOT need a new block contract for this change

### Requirement: Images are resolved as content media, section markers, or unknown media
The classifier SHALL NOT treat every image as opening caption context immediately. It SHALL classify image role using surrounding block relationships.

#### Scenario: Content image with source text forms media group
- **WHEN** an image is followed by source or explanatory text such as `图源大众点评`
- **THEN** the image and source text MUST be resolved as a media group
- **THEN** `图源大众点评` MUST be emitted as `cap`

#### Scenario: Decorative marker with heading text forms section header group
- **WHEN** a decorative or numbered marker image is followed by isolated heading text and then body flow
- **THEN** the marker and heading text MUST be resolved as a section header group
- **THEN** the heading text MUST be emitted as `h`

#### Scenario: Unknown media does not swallow following body by default
- **WHEN** an image does not have enough evidence to be content media or section marker
- **THEN** the classifier MUST NOT automatically classify the next short centered text as `cap`
- **THEN** the next text MUST be resolved by the normal role decision process

### Requirement: Reported URL structures are classified by role relationship
The reported WeChat URL fixtures SHALL be classified by structure, not by hard-coded article URL, title string, color, or absolute font size.

#### Scenario: Number marker title is heading
- **WHEN** the minimal fixture derived from `https://mp.weixin.qq.com/s/gtyfKcOPo6SlcCjYvChhjw` is parsed
- **THEN** `年收入超过11亿元` MUST be emitted as `h`
- **THEN** the following normal paragraph MUST remain `txt`

#### Scenario: Real caption and later section title coexist
- **WHEN** the minimal fixture derived from `https://mp.weixin.qq.com/s/katP22I4qo5CEFWQC5YeVw` is parsed
- **THEN** `图源大众点评` MUST be emitted as `cap`
- **THEN** `重生之泰奶又又又火了` MUST be emitted as `h`

#### Scenario: Explicit h tag in preface is not blindly promoted
- **WHEN** a WeChat preface line such as author or source metadata uses an `h1` tag before body flow starts
- **THEN** the classifier MUST NOT emit it as a body section heading solely because of the tag name

### Requirement: Metamorphic tests prevent sample-specific fixes
The classifier SHALL include tests that prove the implementation is not hard-coded to the reported strings, URLs, wrapper depth, or exact style spelling.

#### Scenario: Heading text can change
- **WHEN** a section-marker fixture replaces `年收入超过11亿元` or `重生之泰奶又又又火了` with another short heading phrase while preserving the same role relationship
- **THEN** the replacement heading MUST still be emitted as `h`

#### Scenario: Wrapper and style spelling can change
- **WHEN** semantically equivalent wrapper spans or sections are inserted, style declarations are reordered, or compatible px/pt units are used
- **THEN** the classifier MUST preserve the same role decisions

#### Scenario: Removing the section marker changes the evidence
- **WHEN** the same short centered text appears without a section marker or body-flow relationship
- **THEN** the classifier MUST NOT promote it to `h` solely because the text is short or centered

### Requirement: Negative role boundaries are preserved
The classifier SHALL preserve known negative boundaries so fixes do not convert captions, source lines, or body emphasis into headings.

#### Scenario: Source lines remain captions after content image
- **WHEN** text following content media begins with `图源`, `图为`, or `图片来源`
- **THEN** it MUST be emitted as `cap`
- **THEN** it MUST NOT be emitted as `h`

#### Scenario: Body-like sentence after marker is not heading by itself
- **WHEN** a section marker is followed by a full sentence or long body-like paragraph
- **THEN** that text MUST NOT be promoted to `h` solely because it follows the marker

#### Scenario: Reference section is not captioned
- **WHEN** a reference heading and reference entries appear after images
- **THEN** the reference heading and entries MUST remain in reference/body flow handling
- **THEN** they MUST NOT be swallowed as image captions
