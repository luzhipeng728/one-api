package model

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/songquanpeng/one-api/common"
	"gorm.io/gorm"
)

type Ability struct {
	Group     string `json:"group" gorm:"type:varchar(32);primaryKey;autoIncrement:false"`
	Model     string `json:"model" gorm:"primaryKey;autoIncrement:false"`
	ChannelId int    `json:"channel_id" gorm:"primaryKey;autoIncrement:false;index"`
	Enabled   bool   `json:"enabled"`
	Priority  *int64 `json:"priority" gorm:"bigint;default:0;index"`
	IsImage   bool   `json:"is_image" gorm:"default:false"`
}

func GetRandomSatisfiedChannel(group string, model string, ignoreFirstPriority bool, isImage bool) (*Channel, error) {
	fmt.Println("GetRandomSatisfiedChannel", group, model, ignoreFirstPriority, isImage)
	ability := Ability{}
	groupCol := "`group`"
	trueVal := "1"
	if common.UsingPostgreSQL {
		groupCol = `"group"`
		trueVal = "true"
	}

	var err error = nil
	var channelQuery *gorm.DB
	if ignoreFirstPriority {
		channelQuery = DB.Where(groupCol+" = ? and model = ? and enabled = "+trueVal, group, model)
	} else {
		maxPrioritySubQuery := DB.Model(&Ability{}).Select("MAX(priority)").Where(groupCol+" = ? and model = ? and enabled = "+trueVal, group, model)
		// 打印下
		fmt.Println(maxPrioritySubQuery.Debug().Error)
		channelQuery = DB.Where(groupCol+" = ? and model = ? and enabled = "+trueVal+" and priority = (?)", group, model, maxPrioritySubQuery)
		// 打印sql
		fmt.Println(channelQuery.Debug().Find(&ability).Error)
		if isImage {
			fmt.Println("这边要过滤掉不是图片的")
			channelQuery = channelQuery.Where("is_image = ?", true)
		} else {
			fmt.Println("这边要过滤掉是图片的")
			channelQuery = channelQuery.Where("is_image = ?", false)
		}
	}

	// 先进行计数查询，确保有满足条件的记录
	var count int64
	if err = channelQuery.Count(&count).Error; err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, fmt.Errorf("no satisfied channel found")
	}

	// 进行实际查询
	if common.UsingSQLite || common.UsingPostgreSQL {
		err = channelQuery.Order("RANDOM()").First(&ability).Error
	} else {
		err = channelQuery.Order("RAND()").First(&ability).Error
	}
	if err != nil {
		return nil, err
	}
	channel := Channel{}
	channel.Id = ability.ChannelId
	err = DB.First(&channel, "id = ?", ability.ChannelId).Error
	return &channel, err
}

func (channel *Channel) AddAbilities() error {
	models_ := strings.Split(channel.Models, ",")
	groups_ := strings.Split(channel.Group, ",")
	abilities := make([]Ability, 0, len(models_))
	for _, model := range models_ {
		for _, group := range groups_ {
			ability := Ability{
				Group:     group,
				Model:     model,
				ChannelId: channel.Id,
				Enabled:   channel.Status == ChannelStatusEnabled,
				Priority:  channel.Priority,
				IsImage:   channel.IsImage,
			}
			abilities = append(abilities, ability)
		}
	}
	return DB.Create(&abilities).Error
}

func (channel *Channel) DeleteAbilities() error {
	return DB.Where("channel_id = ?", channel.Id).Delete(&Ability{}).Error
}

// UpdateAbilities updates abilities of this channel.
// Make sure the channel is completed before calling this function.
func (channel *Channel) UpdateAbilities() error {
	// A quick and dirty way to update abilities
	// First delete all abilities of this channel
	err := channel.DeleteAbilities()
	if err != nil {
		return err
	}
	// Then add new abilities
	err = channel.AddAbilities()
	if err != nil {
		return err
	}
	return nil
}

func UpdateAbilityStatus(channelId int, status bool) error {
	return DB.Model(&Ability{}).Where("channel_id = ?", channelId).Select("enabled").Update("enabled", status).Error
}

func GetGroupModels(ctx context.Context, group string) ([]string, error) {
	groupCol := "`group`"
	trueVal := "1"
	if common.UsingPostgreSQL {
		groupCol = `"group"`
		trueVal = "true"
	}
	var models []string
	err := DB.Model(&Ability{}).Distinct("model").Where(groupCol+" = ? and enabled = "+trueVal, group).Pluck("model", &models).Error
	if err != nil {
		return nil, err
	}
	sort.Strings(models)
	return models, err
}
