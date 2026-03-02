package bind

// Upload marks an element as an upload trigger. When clicked (button)
// or changed (file input), the extension JS collects files and uploads
// them to the server. The action name identifies the upload for
// progress signals and the server callback.
//
//	bind.Upload(button.Text("Upload"), "avatar")
func Upload[E Settable[E]](el E, action string) E {
	return el.SetData("tether-upload", action)
}

// UploadInput sets a CSS selector for finding file inputs when the
// upload trigger is not adjacent to them in the DOM. Without this,
// the JS looks in the closest form or parent element.
//
//	bind.UploadInput(button.Text("Upload"), "#avatar-input")
func UploadInput[E Settable[E]](el E, selector string) E {
	return el.SetData("tether-upload-input", selector)
}

// UploadProgress binds an element's value attribute to the upload
// progress signal. This is a convenience for
// BindAttr(el, "value", "upload:{action}:progress").
//
//	bind.UploadProgress(progress.New().Attr("max", "100"), "avatar")
func UploadProgress[E Settable[E]](el E, action string) E {
	return BindAttr(el, "value", "upload:"+action+":progress")
}
