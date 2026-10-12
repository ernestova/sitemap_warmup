# Separate private repo for the hosted service

The existing public repo keeps its years of history and becomes the open-source Go engine; the hosted service lives in a new private repo that imports the engine as a pinned Go module. A monorepo was the alternative, but separation keeps the open-core boundary obvious and forces the reusable engine into public `pkg/` packages, since Go `internal/` packages cannot be imported across modules.
