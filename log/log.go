// Copyright 2019 tree xie
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package log

import (
	"fmt"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	defaultLogger *zap.Logger
)

type Logger struct{}

func (l *Logger) Errorf(format string, args ...interface{}) {
	str := fmt.Sprintf(format, args...)
	defaultLogger.Error(strings.TrimSpace(str))
}

func (l *Logger) Warningf(format string, args ...interface{}) {
	str := fmt.Sprintf(format, args...)
	defaultLogger.Warn(strings.TrimSpace(str))
}

func (l *Logger) Infof(format string, args ...interface{}) {
	str := fmt.Sprintf(format, args...)
	defaultLogger.Info(strings.TrimSpace(str))
}

func (l *Logger) Debugf(format string, args ...interface{}) {
	str := fmt.Sprintf(format, args...)
	defaultLogger.Debug(strings.TrimSpace(str))
}

func init() {
	c := zap.NewProductionConfig()
	c.DisableCaller = true
	c.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	// 只针对panic 以上的日志增加stack trace
	l, err := c.Build(zap.AddStacktrace(zap.DPanicLevel))
	if err != nil {
		panic(err)
	}
	defaultLogger = l
}

// Default get default logger
func Default() *zap.Logger {
	return defaultLogger
}

// BadgerLogger get badger loggger
func BadgerLogger() *Logger {
	return new(Logger)
}
