package diagram

// CSS is what a standalone file embeds and what the studio stylesheet
// includes: hover and focus only. The picture is complete without it.
const CSS = `.more, .less { cursor: pointer }
.more:hover, .less:hover, .more:focus, .less:focus { text-decoration: underline }
.edge:hover path { stroke: #1f5c4f; stroke-width: 2 }
.step:hover > rect.box { stroke: #36577a }
.popover { cursor: default }
.popover-close { cursor: pointer }
`
