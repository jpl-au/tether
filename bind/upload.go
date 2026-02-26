package bind

// Upload marks an element as an upload trigger. When clicked (button)
// or changed (file input), the extension JS collects files and uploads
// them to the server. The action name identifies the upload for
// progress signals and the server callback.
//
//	bind.Upload(button.Text("Upload"), "avatar")
func Upload[E Settable[E]](el E, action string) E {
	return el.SetData("poly-upload", action)
}

// UploadProgress binds an element's value attribute to the upload
// progress signal. This is a convenience for
// BindAttr(el, "value", "upload:{action}:progress").
//
//	bind.UploadProgress(progress.New().Attr("max", "100"), "avatar")
func UploadProgress[E Settable[E]](el E, action string) E {
	return BindAttr(el, "value", "upload:"+action+":progress")
}
