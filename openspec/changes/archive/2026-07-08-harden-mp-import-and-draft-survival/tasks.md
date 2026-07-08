## 1. Failing Regression Tests

- [x] 1.1 Capture minimal HTML fixtures from the two reported WeChat URLs, preserving the deciding context: previous content image, caption, decorative marker image, heading text, and following body paragraph
- [x] 1.2 Add a failing Go test proving the `gtyf...` fixture emits `年收入超过11亿元` as `h` and the following body paragraph as `txt`
- [x] 1.3 Add a failing Go test proving the `katP22...` fixture emits `图源大众点评` as `cap` and `重生之泰奶又又又火了` as `h` in the same article
- [x] 1.4 Add failing Go tests proving an author/source preface using `<h1>` is not blindly emitted as a body heading
- [x] 1.5 Add failing metamorphic Go tests that replace heading text, remove URL-specific values, add wrapper spans/sections, reorder style declarations, and change px/pt units without changing the expected role
- [x] 1.6 Add failing negative Go tests where the same short text is `cap` after a content image but not after a decorative section marker, and where a full sentence after a marker does not automatically become `h`
- [x] 1.7 Add failing JS tests for immediate WeChat draft save timing where external HTTP `<img src>` is not treated as already transferred
- [x] 1.8 Add failing JS tests proving公众号 more-articles copy-safe card `<img src>` is mmbiz or bitmap data, not OSS/external HTTP as the only immediate-safe representation
- [x] 1.9 Add or update a runtime copy test with a controllable pending preparation promise, proving clipboard write happens before the background job resolves

## 2. URL Block Classification Model

- [x] 2.1 Add an internal `ObservedBlock` representation that records DOM-order facts without deciding `h` / `txt` / `cap`
- [x] 2.2 Add an `ArticleProfile` builder that infers article-local body flow, repeated body style, caption-like subordinate text, and reference-section candidates
- [x] 2.3 Add a `RoleDecision` resolver that recognizes `MediaGroup`, `SectionHeaderGroup`, `RefGroup`, `StandaloneHeading`, `BodyFlow`, and `Unknown`
- [x] 2.4 Stop opening global caption context immediately on every image; classify images as content image, section marker, or unknown media after limited lookahead
- [x] 2.5 Convert `RoleDecision` back to the existing public `Block` JSON shape so frontend renderers remain unchanged
- [x] 2.6 Expose role decision reasons to Go tests only, so tests can assert structure-based reasons rather than hard-coded text matches
- [x] 2.7 Preserve existing reference-section protections and ordinary caption behavior for true content images
- [x] 2.8 Run `cd api && go test ./...` and confirm all new URL classification tests pass without weakening existing tests

## 3. More-Articles Draft Survival

- [x] 3.1 Extend the WeChat draft simulator with `immediate` and `delayed` modes; immediate mode must not transfer external HTTP `<img src>`
- [x] 3.2 Define and implement a公众号 more-articles copy-safe predicate: `imgurl` is WeChat-hosted and card `<img src>` is WeChat-hosted or bitmap data
- [x] 3.3 Update公众号 copy-safe rendering so populated cards default to the original mmbiz cover when no safe bitmap composite is already available
- [x] 3.4 Prevent OSS/external HTTP composite URLs from being written as the only card `<img src>` in公众号 copy-safe output
- [x] 3.5 Define the non-WeChat-cover fallback: do not emit a fake-safe `linktype=image` card when no WeChat-hosted `imgurl` is available
- [x] 3.6 Preserve preview rendering and FoodTalks website-channel behavior; the stricter predicate applies only to公众号 copy output
- [x] 3.7 Preserve `_footerCopyGen` and `_moreArticlesCopySeq` guards so stale background jobs cannot overwrite a newer article or footer copy layer
- [x] 3.8 Run `npm test -- tests/wechat-draft-survival.test.mjs tests/copy-behavior.test.mjs` and confirm new tests pass

## 4. End-to-End Verification

- [x] 4.1 Run full verification: `cd api && go test ./...` and `npm test`
- [x] 4.2 Start the local API server and import both reported WeChat URLs through the real UI
- [x] 4.3 Verify in preview that `年收入超过11亿元` and `重生之泰奶又又又火了` render as headings, while `图源大众点评` renders as a caption
- [x] 4.4 Copy the article output and inspect clipboard HTML to confirm more-articles cards satisfy the T0 safety predicate
- [ ] 4.5 If a logged-in WeChat editor is available, paste and save immediately, then save again after waiting, confirming the more-articles section does not disappear in either timing

## 5. Documentation

- [x] 5.1 Add a concise WORKLOG documenting the multi-agent rejection, the new three-layer URL classification model, and the draft-save T0 safety invariant
- [x] 5.2 Update existing guardrail documentation only if the implementation changes the current微信复制安全契约 wording
