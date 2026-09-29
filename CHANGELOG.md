# Changelog

All notable changes to At The Rack are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project aims
to follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

Everything built so far, ahead of the first tagged release.

### Added

- **AI coach.** Chat with Claude to log and review your training in plain
  language. The coach reads and writes your data through tools, so answers use
  your real numbers. Conversations are organized into threads with automatic
  titles, rename, and delete. Attach, paste, or drop up to 4 photos per message
  to ask about a machine, a meal, a physique check-in, or a plan. Replies run in
  the background, so they finish and save even if you navigate away or reload,
  and a failed reply shows a **Retry** button that re-runs the last turn in
  place — no need to retype it.
- **Training.** Log sets (exercise, weight, reps, RPE) grouped into dated
  workouts, with an optional **note per set**. Search a library of ~870
  exercises by name, muscle, or equipment, or add your own (an AI button infers
  muscles and equipment from the name). A per-exercise history drawer shows your
  best set, estimated 1RM, a progression chart, and every set by date.
- **Reusable programs.** Build a workout program once and start it into a day's
  log with one click.
- **Cardio, Bodyweight, and Progress tabs.** Log cardio sessions with pace and
  distance history; track weigh-ins with a trend chart and stats; record body
  measurements with per-site charts and keep a dated gallery of progress photos.
- **Diet and Supplements tabs.** Log food with optional notes and macros, and
  log supplement doses that become one-click buttons with a 30-day consistency
  view.
- **Self-hosting.** Ships as a single Go binary over a local SQLite file, with
  no build step and no external services beyond the optional Anthropic API.
  Docker and Docker Compose packaging is included.
- **Data import.** One-shot importers for ChimpFitness CSV exports (exercises,
  workouts, bodyweight, and cardio), plus a `-seed-demo` mode that fills an
  empty database with about six weeks of realistic fake data.

### Changed

- **Visual refresh.** Reworked the interface to feel deliberately designed
  rather than assembled from a component kit: dropped the bordered card chrome
  from the data tabs, turned the stat blocks into clean typographic figure rows,
  flattened the exercise-library suggestions into plain text filters, and
  tightened the training log so each set reads as one line.
- **Mobile.** Made every screen usable on a phone, with proper touch targets,
  no-zoom inputs, and safe-area handling.

### Developer

- Browser end-to-end tests (Playwright, against a fake Claude API) cover every
  tab and the coach's tool use, and run cross-platform on Windows, macOS, and
  Linux. Continuous integration runs formatting, vetting, unit tests, and the
  e2e suite on every push and pull request.

[Unreleased]: https://github.com/jwald3/attherack/commits/main
