//go:build !unix

package coder

func processAlive(int) bool { return false }
