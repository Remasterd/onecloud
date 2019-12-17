// Copyright 2019 Yunion
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

package models

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io/ioutil"
	"os"
	"strings"
	"sync"
	ptem "text/template"

	"yunion.io/x/jsonutils"
	"yunion.io/x/pkg/errors"

	"yunion.io/x/onecloud/pkg/cloudcommon/db"
	"yunion.io/x/onecloud/pkg/httperrors"
	"yunion.io/x/onecloud/pkg/mcclient"
	"yunion.io/x/onecloud/pkg/notify/rpc/apis"
	"yunion.io/x/onecloud/pkg/notify/template"
)

type STemplateManager struct {
	SStandaloneResourceBaseManager
}

var TemplateManager *STemplateManager

func init() {
	TemplateManager = &STemplateManager{
		SStandaloneResourceBaseManager: NewStandaloneResourceBaseManager(
			STemplate{},
			"notify_t_template",
			"notifytemplate",
			"notifytemplates",
		),
	}
}

const (
	TEMPLATE_TYPE_TITLE   = "title"
	TEMPLATE_TYPE_CONTENT = "content"
	TEMPLATE_TYPE_REMOTE  = "remote"
)

var (
	DefaultEmailUrl = ""
	EmailUrl        = ""
	EmailUrlLock    sync.RWMutex
)

type STemplate struct {
	SStandaloneResourceBase

	ContactType string `width:"16" nullable:"false" create:"required" update:"user" list:"user"`
	Topic       string `width:"20" nullable:"false" create:"required" update:"user" list:"user"`

	// title | content | remote
	TemplateType string `width:"10" nullable:"false" create:"required" update:"user" list:"user"`
	Content      string `length:"text" nullable:"false" create:"required" get:"user" list:"user"`
}

func (tm *STemplateManager) GetEmailUrl() string {
	EmailUrlLock.RLock()
	defer EmailUrlLock.RUnlock()
	if len(EmailUrl) == 0 {
		return DefaultEmailUrl
	}
	return EmailUrl
}

func (tm *STemplateManager) SetEmailUrl(url string) {
	EmailUrlLock.Lock()
	defer EmailUrlLock.Unlock()
	EmailUrl = url
}

func (tm *STemplateManager) InitializeData() error {
	q := tm.Query().Equals("contact_type", "email").Equals("topic", "VERIFY").Equals("template_type", "content")
	count, _ := q.CountWithError()
	if count > 0 {
		return nil
	}
	content, err := ioutil.ReadFile(template.EMAIL_VERIFY_CONTENT_PATH)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return errors.Wrapf(err, "os.Open for '%s'", template.EMAIL_VERIFY_CONTENT_PATH)
	}
	contentTem := STemplate{
		ContactType:  "email",
		Topic:        "VERIFY",
		TemplateType: "content",
		Content:      string(content),
	}
	titleTem := STemplate{
		ContactType:  "email",
		Topic:        "VERIFY",
		TemplateType: "title",
		Content:      template.EMAIL_VERIFY_TITLE,
	}
	err = tm.TableSpec().InsertOrUpdate(&contentTem)
	if err != nil {
		return errors.Wrap(err, "sqlchemy.TableSpec.InsertOrUpdate")
	}
	tm.TableSpec().InsertOrUpdate(&titleTem)
	if err != nil {
		return errors.Wrap(err, "sqlchemy.TableSpec.InsertOrUpdate")
	}
	return nil
}

// NotifyFilter will return the title and content generated by corresponding template.
// Local cache about common template will be considered in case of performance issues.
func (tm *STemplateManager) NotifyFilter(contactType, topic, msg string) (params apis.SendParams, err error) {
	params.Topic = topic
	templates := make([]STemplate, 0, 3)
	q := tm.Query().Equals("contact_type", contactType).Equals("topic", strings.ToUpper(topic))
	err = db.FetchModelObjects(tm, q, &templates)
	if errors.Cause(err) == sql.ErrNoRows || len(templates) == 0 {
		// no such template, return as is
		params.Title = topic
		params.Message = msg
		return
	}
	if err != nil {
		err = errors.Wrap(err, "db.FetchModelObjects")
		return
	}
	for _, template := range templates {
		var title, content string
		switch template.TemplateType {
		case TEMPLATE_TYPE_TITLE:
			title, err = template.Execute(msg)
			if err != nil {
				return
			}
			params.Title = title
		case TEMPLATE_TYPE_CONTENT:
			content, err = template.Execute(msg)
			if err != nil {
				return
			}
			params.Message = content
		case TEMPLATE_TYPE_REMOTE:
			params.RemoteTemplate = template.Content
			params.Message = msg
		default:
			err = errors.Error("no support template type")
			return
		}
	}
	return
}

func (tm *STemplate) Execute(str string) (string, error) {
	tem, err := ptem.New("tmp").Parse(tm.Content)
	if err != nil {
		return "", errors.Wrapf(err, "Template.Parse for template %s", tm.GetId())
	}
	var buffer bytes.Buffer
	tmpMap := make(map[string]interface{})
	err = json.Unmarshal([]byte(str), &tmpMap)
	if err != nil {
		return "", errors.Wrap(err, "json.Unmarshal")
	}
	err = tem.Execute(&buffer, tmpMap)
	if err != nil {
		return "", errors.Wrap(err, "template,Execute")
	}
	return buffer.String(), nil
}

func (manager *STemplateManager) ValidateCreateData(ctx context.Context, userCred mcclient.TokenCredential,
	ownerId mcclient.IIdentityProvider, query jsonutils.JSONObject, data *jsonutils.JSONDict) (*jsonutils.JSONDict, error) {

	ty, _ := data.GetString("template_type")
	if ty != TEMPLATE_TYPE_TITLE && ty != TEMPLATE_TYPE_CONTENT && ty != TEMPLATE_TYPE_REMOTE {
		return nil, httperrors.NewInputParameterError("no such support for tempalte type %s", ty)
	}
	return data, nil
}
