package qr

import qrcode "github.com/skip2/go-qrcode"

func ASCII(text string) (string, error) {
	q, err := qrcode.New(text, qrcode.Medium)
	if err != nil {
		return "", err
	}
	return q.ToSmallString(false), nil
}
