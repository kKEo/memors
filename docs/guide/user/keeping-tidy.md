# Keeping it tidy

A memory that grows for months collects loose ends:

- facts that contradict each other;
- two names for the same thing, such as "Postgres" and "PostgreSQL";
- near-duplicate notes;
- topics mentioned in many places but never summarised.

memors-mcp finds these. It does not fix them on its own. Claude does the writing, and you decide
anything that matters.

## Asking Claude to tidy up

> Tidy up the memors knowledge base.

Claude asks memors-mcp for a to-do list. Each item comes with the material needed to handle it:

- **Write a summary page** for a topic mentioned in many places. Claude writes it from the
  saved passages. Each sentence points back to its source.
- **Refresh an out-of-date page.** A summary is marked *stale* when a document it was based on
  changes.
- **Resolve a contradiction** between two facts by keeping the right one.
- **Merge two names** that mean the same thing.
- **Build the book.** Once a few summary pages exist, Claude is asked to group them into
  chapters, then to write each chapter, then an overview. See [The book](#the-book) below.

## Checks on every summary

When Claude hands a summary back, memors-mcp checks it before keeping it:

- **Did it leave anything out?** Important facts about the topic that the page does not
  mention are reported.
- **Did it make anything up?** Sentences that the sources do not support are flagged.
- **What changed?** For a refreshed page, you see the difference from the previous version.

Summary pages are always labelled as written by Claude, with their sources listed, so they
never pass for original material.

In the web face (`memors-mcp ui`) a summary page shows as formatted text, and every `memo://`
citation in it is a link to the passage it came from. For a browsable site of the whole memory,
summaries included, export it and build it as a book. If the memory has a [book](#the-book),
the site follows its chapters:

```bash
memors-mcp export --md ~/kb-export && mdbook build ~/kb-export
```

## The book

Summary pages answer "what is this thing?". The book answers "where do I start?". It has three
levels, and you can stop reading at any of them:

1. **The overview**: one page on what this memory covers and how its parts fit together.
2. **Chapters**: one page per part, which introduces the topics in it and quotes examples from
   your documents.
3. **Summary pages and documents**: the detail, linked from each chapter.

Ask for it like this:

> Build the book for the memors knowledge base.

Claude works from the bottom up, and memors-mcp only offers the next step when the one below is
done:

1. It writes the summary pages that are still missing.
2. It proposes an **outline**: the chapters, a sentence on what each is about, and which topics
   and documents belong to it. You can ask to see it first, or edit it yourself with
   `memors-mcp outline show` and `memors-mcp outline apply`.
3. It writes a page for each **chapter**.
4. It writes the **overview**.

Chapters get two extra checks:

- **Is every topic of the chapter mentioned?** A topic the chapter does not name is reported.
- **Are the examples real?** Every quoted block must appear word for word in one of your
  documents. memors-mcp can show an example was copied faithfully; it cannot run it.

The book stays current without being rewritten all the time. When one summary page changes, its
chapter is left alone. When two have changed, the chapter is marked stale and Claude is asked to
refresh it; the overview follows the chapters in the same way. Changing the outline marks the
chapters you touched.

A chapter you wrote or edited yourself is yours: Claude may add topics to it, and cannot rename
it, remove it or take topics out of it.

Read the book in the web face under **Book**, or export it (below).

## Decisions that stay with you

Merging two names and settling contradictions affect what the memory treats as true. Claude can
propose them, and you can confirm them in the conversation or from the terminal. Nothing is
merged silently.

> **Tip.** Once a month, ask "Is there anything in memors that needs my decision?" Claude will
> list the open items.
