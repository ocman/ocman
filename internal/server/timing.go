package server

// Server-Timing collection lives in internal/srvtiming so adapter
// code (internal/platforms/...) can record phases without depending
// on the server package. The HTTP middleware in middleware.go
// attaches a fresh Collector per request and renders its accumulated
// entries into the Server-Timing response header on the way out.
//
// This file used to host the implementation; it's intentionally kept
// as documentation so a reader following imports lands here first.
