package custommiddleware

import (
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo"
)

func TestInitialization(t *testing.T) {
	conf := InitializationConfig{
		Header: []string{
			"X-Token:ABCD",
		},
	}
	fn := Initialization(conf)(func(c echo.Context) error {
		if c.Response().Header().Get("X-Token") != "ABCD" {
			t.Fatalf("set header in init function fail")
		}
		return nil
	})
	resp := &httptest.ResponseRecorder{}
	e := echo.New()
	c := e.NewContext(nil, resp)
	err := fn(c)
	if err != nil {
		t.Fatalf("initialization fail")
	}
}