# Shipyard design system

Shipyard is an operator console: calm surfaces make work readable; signal colours are reserved for action, decisions and failures.

## Contract

- Use semantic tokens from `app.css`; do not add raw palette colours in components.
- Use the spacing scale (`--space-1` through `--space-5`) and radius scale only.
- Forms use native controls. Every interactive control has a visible focus state and a text label or accessible name.
- The global shell owns navigation, branding and responsive behaviour. Page templates must not redefine sidebar geometry.
- Modals use native `dialog`, return focus to their trigger and close on Escape.
- Sidebar links remain ordinary links. Tab is canonical; arrow keys/Home/End are an additional convenience when focus is in the sidebar.
- New screens require light, dark, desktop and tablet visual snapshots before acceptance.

## Foundations and token layers

Shipyard uses three layers. This follows the practical split between shared
foundations, semantic system decisions and component decisions described by
modern design systems; token names describe intent, never a literal colour.

1. **Foundation tokens** define the bounded spacing scale, radii and base
   palette in `:root`.
2. **Semantic tokens** (`--canvas`, `--surface`, `--ink`, `--line`,
   `--accent`, `--danger`, `--success`) express a UI role and are the only
   palette tokens templates/components may consume.
3. **Component tokens** are permitted only when a component has a reusable
   role that cannot be expressed by the semantic layer (for example
   `--nav-active`). They must resolve to semantic/foundation tokens and have
   light and dark values together.

Token additions need a plain-language purpose, a dark-mode value and a
consumer. Do not create a token for a one-off page adjustment. The canonical
token definitions live in `internal/web/static/app.css`; a future export may
use the DTCG JSON token format, whose text-based interchange model is suited
to sharing design decisions between tools.

## Component rules

- A page has one primary reading path: header, current action/summary, then
  supporting detail. Do not place creation forms permanently next to a list.
- Use broad surfaces only when they group a distinct job. Lists use separators
  by default; cards signal a self-contained decision or object, not spacing.
- Primary actions are unique per region. Secondary and destructive actions
  stay visually quieter and never compete with the primary action.
- Inputs pair a persistent visible label with help/error text close to the
  field. Placeholder text is an example, never the label.
- Status has text and, where useful, an icon or shape in addition to colour.
- Empty states name the next useful action. Loading and error states name what
  happened and what the user can try.
- New shared patterns first receive a semantic CSS class and a documented
  purpose; do not style an element through page-specific selector chains.

## Layout and interaction invariants

- The app shell is the only owner of the sidebar. It must remain toggleable on desktop, become a hamburger menu on narrow screens, and its link list must scroll independently when vertical space is limited.
- Pages use one content rhythm: a page header, then sections separated by `--space-4`; cards use `--space-3` internally. Do not introduce one-off margins to fix an individual view.
- Lists are for inspection. Creation and edits happen in a native modal or a dedicated page, never as a permanent form beside a list.
- Destructive actions are visually secondary, require a confirmation, and must not sit in the primary action position.
- Every state-changing form receives a visible success redirect or an in-place error toast. A rejected request must never look like an inactive button.
- Management cards show the primary explanation first, then status and controls. Keep controls aligned to a card edge and avoid repeating the same action in the body.
- Charts communicate a time window and a legend. Empty charts explain what data is missing and what action will produce it.

## Review gate

Before accepting a UI change, verify at least one desktop and one tablet/mobile viewport, keyboard focus, open/close behaviour for any dialog, and the error path of each changed form. Run `deploy/verify-production.sh` after deployment; its UI smoke check is part of the release gate.

## Signals

Accent means an available action, amber means a human decision is required, red means a failure, and green means completion. Do not use colour as the only status indicator.
