package main

type WindowInfo struct {
	HWND  uintptr `json:"hwnd"`
	Title string  `json:"title"`
}
