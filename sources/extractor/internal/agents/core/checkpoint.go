package core

import "github.com/mohammad-safakhou/diffmind/internal/runstate"

// Deprecated compatibility aliases. Pipeline stages use runstate directly.
type (
	CheckpointStore          = runstate.CheckpointStore
	DetailCheckpointEntry    = runstate.DetailCheckpointEntry
	DiscoveryCheckpointEntry = runstate.DiscoveryCheckpointEntry
	ReexamCheckpointEntry    = runstate.ReexamCheckpointEntry
)

const StateDir = runstate.StateDir

var (
	DetailEntityKey = runstate.DetailEntityKey
	ReexamKey       = runstate.ReexamKey
)
