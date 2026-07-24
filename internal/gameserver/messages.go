package gameserver

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"time"

	"github.com/tianyu150/mir-eternal/internal/gameprotocol"
	"github.com/tianyu150/mir-eternal/internal/gamestore"
)

const agreementBase64 = "MzUyNjM1MTk4MAA4MAA6MjU6YWE6M2Y6MjI6MDYAVUxTMjEtNTUzMmUwNjkyZDIzNGJiMDlhOGJjMmRmZGY5YTMBgW4GAAAHAAAAMjAxNi0wMi0yNgB4nO1WzWoUQRDu7flb92dmXaNBIhqMbMRoUJQIehAl5ODNeFY8ePMtBMXn8OLFZ9CDb+LFizcfIFbtfJ/zbZNERDGXLSiqu7r+u6tmQgihH46H2vAA8FHWQ1tflj2hzELY74VQ4sztl4fIORRHnDnvtGE0XDUcIM4G8iPgGHJ+PsF+DNkKeBc5uPwKfFWgb6B3z/AZeK7vuV0RG47b8F3BXgm/Y5HZgm/nPze0UoRThjnkC5ydl5wvwEYBGyX0aslbazQQuTJZn4WO03PQGYBeNDqFH+qx1nlyB1F0c8Tn9dsVWa2x2/iSdX5o5wzWI9S0AvX8NiWGIfilIP3XsmbNKVMgnkpiyiBTSRwxtjlkQMZN2fWkvk4b1Iv34Lxbcvesk5/tiE6FmIYSN/UZP5HvQnNalfVUYqxFrpZz5lnhPAKdR/2ZvCtiKbK+vyR3OQBvAuoyfck5JroRueTYN7LPJU7mMrdTtPeXJbVnnmnNSJk7859i3b/a6ekb2hA7WoNG3gL9v7T1muGdsNgfjI3x82419lrqTj96Vw8Nr0t+lPX9h9jGM5L495DbNXkH7FXencsuYQl/A4/C4ozzt3UDe2upeS/cDm0/fMpb2a3Q9cQm1vtYu/4MqH03EB32jvYeZ1j5GxwdwY/H8Ktk777ex64360TnZmjnAGP6FhZnAeV2QB8Yboht/S5yZuhsoo1Z6OZikdSCfe7fg1ei57AWuu+M8312DAU5p3U2zWek1PzXHOq19F3sZuoQNWkSGwcJ6Gwk77Pk+TW0/4jcc769TfSc7ib2/X747dqzGLdtf3/d1vYgHxvvR2+xJpOkRlpnp99NfyVrZZ6GJSxhCf8DyhPy63Pt9Qn5/pfAuet1fGLz60U4/LvnczL7A7s/AelK6Z4="
const selectorBase64 = "AAAzNTI2MzUxOTgwADgwADoyNTphYTozZjoyMjowNgBVTFMyMQA1NTMyZTA2OTJkMjM0YmIwOWE4YmMyZGZkZjlhMwGBbgYAAAcAAAAyMDE2LTAyLTI2AHic7VbNahRBEO7t+Vv3Z2Zdo0EiGoxsxGhQlAh6ECXk4M14Vjx48y0Exefw4sVn0INv4sWLNx8gVu18n/Ntk0REMZctKKq7uv67q2ZCCKEfjofa8ADwUdZDW1+WPaHMQtjvhVDizO2Xh8g5FEecOe+0YTRcNRwgzgbyI+AYcn4+wX4M2Qp4Fzm4/Ap8VaBvoHfP8Bl4ru+5XREbjtvwXcFeCb9jkdmCb+c/N7RShFOGOeQLnJ2XnC/ARgEbJfRqyVtrNBC5MlmfhY7Tc9AZgF40OoUf6rHWeXIHUXRzxOf12xVZrbHb+JJ1fmjnDNYj1LQC9fw2JYYh+KUg/deyZs0pUyCeSmLKIFNJHDG2OWRAxk3Z9aS+ThvUi/fgvFty96yTn+2IToWYhhI39Rk/ke9Cc1qV9VRirEWulnPmWeE8Ap1H/Zm8K2Ipsr6/JHc5AG8C6jJ9yTkmuhG55Ng3ss8lTuYyt1O095cltWeeac1ImTvzn2Ldv9rp6RvaEDtag0beAv2/tPWa4Z2w2B+MjfHzbjX2WupOP3pXDw2vS36U9f2H2MYzkvj3kNs1eQfsVd6dyy5hCX8Dj8LijPO3dQN7a6l5L9wObT98ylvZrdD1xCbW+1i7/gyofTcQHfaO9h5nWPkbHB3Bj8fwq2Tvvt7HrjfrROdmaOcAY/oWFmcB5XZAHxhuiG39LnJm6GyijVno5mKR1IJ97t+DV6LnsBa674zzfXYMBTmndTbNZ6TU/Ncc6rX0Xexm6hA1aRIbBwnobCTvs+T5NbT/iNxzvr1N9JzuJvb9fvjt2rMYt21/f93W9iAfG+9Hb7Emk6RGWmen301/JWtlnoYlLGEJ/wPKE/Lrc+31Cfn+l8C563V8YvPrRTj8u+dzMvsDuz8B6UrpnoGBgYGBgYGBgYGBtOivgYGBgYGBgYGBgYGBgYGBgYGLgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgQ=="

var agreement = mustBase64(agreementBase64)
var selectorTemplate = mustBase64(selectorBase64)

func mustBase64(value string) []byte {
	result, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		panic(err)
	}
	return result
}

func loginSuccessPacket() ([]byte, error)  { return gameprotocol.BuildVariable(1002, agreement) }
func serviceStatusPacket() ([]byte, error) { return gameprotocol.Build(1012, nil) }
func tuningPacket() ([]byte, error) {
	return gameprotocol.Build(692, func(data []byte) {
		values := []uint16{100, 130, 160, 190, 220, 250, 250}
		for i, value := range values {
			binary.LittleEndian.PutUint16(data[2+i*2:], value)
		}
	})
}
func extensionPacket() ([]byte, error) { return gameprotocol.BuildVariable(693, nil) }
func loginErrorPacket(code uint32, argument1, argument2 int32) ([]byte, error) {
	return gameprotocol.Build(1001, func(data []byte) {
		binary.LittleEndian.PutUint32(data[2:6], code)
		binary.LittleEndian.PutUint32(data[6:10], uint32(argument1))
		binary.LittleEndian.PutUint32(data[10:14], uint32(argument2))
	})
}
func integerPacket(id uint16, value int32) ([]byte, error) {
	return gameprotocol.Build(id, func(data []byte) { binary.LittleEndian.PutUint32(data[2:6], uint32(value)) })
}

func characterListPacket(characters []gamestore.Character) ([]byte, error) {
	if len(selectorTemplate) != 847 {
		return nil, errors.New("invalid selector template")
	}
	description := append([]byte(nil), selectorTemplate...)
	if len(characters) > 9 {
		characters = characters[:9]
	}
	description[0] = byte(len(characters))
	for i, character := range characters {
		copy(description[1+i*94:1+(i+1)*94], characterDescription(character))
	}
	return gameprotocol.Build(1004, func(data []byte) { copy(data[2:], description) })
}
func characterCreatedPacket(character gamestore.Character) ([]byte, error) {
	description := characterDescription(character)
	return gameprotocol.Build(1005, func(data []byte) { copy(data[2:], description) })
}
func characterDescription(character gamestore.Character) []byte {
	data := make([]byte, 94)
	binary.LittleEndian.PutUint32(data[0:4], uint32(character.ID))
	name := []byte(character.Name)
	if len(name) > 56 {
		name = name[:56]
	}
	copy(data[4:60], name)
	data[61] = character.Race
	data[62] = character.Gender
	data[63] = character.Hair
	data[64] = character.HairColor
	data[65] = character.Face
	data[67] = character.Level
	binary.LittleEndian.PutUint32(data[68:72], uint32(character.MapID))
	binary.LittleEndian.PutUint32(data[85:89], uint32(protocolTime(character.OfflineAt)))
	if !character.FrozenAt.IsZero() {
		binary.LittleEndian.PutUint32(data[89:93], uint32(protocolTime(character.FrozenAt)))
	}
	return data
}
func protocolTime(value time.Time) int32 {
	if value.IsZero() {
		return 0
	}
	return int32(value.Unix())
}
