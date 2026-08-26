package main

// RewriteExpect is [rewriteExpect], the skill handshake's rewriter, so the
// external test can drive it on a file of its own rather than through Run's
// whole tree of manifests.
var RewriteExpect = rewriteExpect
