// Package storage persists fibe-distilled domain objects in SQLite.
//
// It owns schema migration, row scanning, JSON field encoding, uniqueness
// checks, and repository-style methods for Marquees, Props, Playspecs,
// Playgrounds, BuildRecords, AsyncOperations, and server metadata.
package storage
