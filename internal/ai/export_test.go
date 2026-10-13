package ai

// EnforceDraftCapsForTest exposes enforceDraftCaps to the external ai_test
// package. [F205-04] decision_drafter_seam_test.go has to live in package
// ai_test because it imports internal/proposal, which imports internal/decision,
// which imports internal/ai — an in-package test would be an import cycle.
var EnforceDraftCapsForTest = enforceDraftCaps
