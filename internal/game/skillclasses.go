package game

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ggmolly/belfast/internal/connection"
	"github.com/ggmolly/belfast/internal/db"
	"github.com/ggmolly/belfast/internal/gamedata"
	"github.com/ggmolly/belfast/internal/protobuf"
)

// The Tactical Class (view/navalacademy/newtactics/, NavalAcademyProxy
// students) is where ships level their skills: a lesson book in a room for
// the book's time, then its exp on the skill (Ship:addSkillExp). No official
// capture has a lesson: the replies are built from the Lua alone.

// itemTypeLesson is Item.LESSON_TYPE (model/vo/item.lua), the skill books.
const itemTypeLesson = 10

// maxSkillClassNum is NavalAcademyProxy.MAX_SKILL_CLASS_NUM, the rooms the
// shop_template skill_room_pos goods open up to.
const maxSkillClassNum = 4

// Student.CANCEL_TYPE_*: how CS_22203 ends a lesson. The client sends AUTO
// when the lesson's timer runs out (NewNavalTacticsShipCard:OnFinish) and
// MANUAL when the commander stops it early (tactics_lesson_cancel "only
// partial EXP will be granted and you will lose your textbook").
const (
	cancelTypeAuto   = 0
	cancelTypeManual = 1
)

// activityTypeBuff is ActivityConst.ACTIVITY_TYPE_BUFF: its buffs are
// config_id, or config_data when config_id is 0 (BuffHelper.GenBuffsForActivity).
const activityTypeBuff = 30

// benefitSkillLearnTime is the benefit_buff_template benefit_type whose
// benefit_effect is how many lessons a day finish instantly
// (NavalAcademyProxy:getDailyFinishCnt, CS_22014).
const benefitSkillLearnTime = "skill_learn_time"

// lessonBook is an item_data_statistics lesson's usage_arg: [time (s), exp,
// skill_data_template type, exp bonus (%) on a skill of that type]
// (NewNavalTacticsSelLessonPage:UpdateLesson).
type lessonBook struct {
	time, exp, skillType, bonus uint32
}

// skillConfig is a skill_data_template row: ShipSkill's maxLevel and the
// type the books' bonus checks.
type skillConfig struct {
	MaxLevel uint32 `json:"max_level"`
	Type     uint32 `json:"type"`
}

var (
	lessonBooks map[uint32]lessonBook
	skills      map[uint32]skillConfig
	// skillNeedExp is skill_need_exp's exp by level: what a skill at that
	// level needs to reach the next one (0 for the last level)
	skillNeedExp map[uint32]uint32
	// skillLearnBuffs are the quick finishes of each skill_learn_time
	// benefit_buff_template row
	skillLearnBuffs map[uint32]uint32
)

func loadSkillClassData() error {
	var needExp map[uint32]struct {
		Exp uint32 `json:"exp"`
	}
	var buffs map[uint32]struct {
		BenefitType   string `json:"benefit_type"`
		BenefitEffect string `json:"benefit_effect"`
	}
	err := gamedata.LoadAll(
		gamedata.Load("skill_data_template", &skills),
		gamedata.Load("skill_need_exp", &needExp),
		gamedata.Load("benefit_buff_template", &buffs),
	)
	if err != nil {
		return err
	}
	if len(needExp) == 0 {
		return gamedata.Missing("skill_need_exp", "levels")
	}
	skillNeedExp = make(map[uint32]uint32, len(needExp))
	for level, row := range needExp {
		skillNeedExp[level] = row.Exp
	}
	skillLearnBuffs = map[uint32]uint32{}
	for id, buff := range buffs {
		if buff.BenefitType != benefitSkillLearnTime {
			continue
		}
		if n, err := strconv.ParseUint(buff.BenefitEffect, 10, 32); err == nil {
			skillLearnBuffs[id] = uint32(n)
		}
	}
	return nil
}

// readLessonBooks reads the usage_arg of the item_data_statistics lessons.
func readLessonBooks(items map[uint32]itemConfig) {
	lessonBooks = map[uint32]lessonBook{}
	for id, row := range items {
		var args []uint32
		if row.Type != itemTypeLesson || json.Unmarshal(row.UsageArg, &args) != nil || len(args) < 4 {
			continue
		}
		lessonBooks[id] = lessonBook{time: args[0], exp: args[1], skillType: args[2], bonus: args[3]}
	}
}

// lessonExp is what book teaches skillID in a whole lesson.
func lessonExp(book lessonBook, skillID uint32) uint32 {
	rate := uint32(100)
	if skills[skillID].Type == book.skillType {
		rate += book.bonus
	}
	return book.exp * rate / 100
}

// skillMaxLevel is the level a skill stops at: its max_level, and never past
// skill_need_exp's last level (Ship:addSkillExp).
func skillMaxLevel(skillID uint32) uint32 {
	return min(skills[skillID].MaxLevel, uint32(len(skillNeedExp)))
}

// addSkillExp adds exp to one of the commander's ship's skill as
// Ship:addSkillExp does: levels while the exp covers skill_need_exp, the exp
// dropped at the last level.
func (tx *Tx) addSkillExp(shipID, skillID, exp uint32) error {
	skill, err := tx.GetShipSkill(tx.ctx, db.GetShipSkillParams{CommanderID: tx.commanderID, ShipID: shipID, SkillID: skillID})
	if errors.Is(err, sql.ErrNoRows) {
		return errRefused
	} else if err != nil {
		return err
	}
	top := skillMaxLevel(skillID)
	if skill.Level >= top {
		return nil
	}
	level, total := skill.Level, skill.Exp+exp
	for total >= skillNeedExp[level] {
		total -= skillNeedExp[level]
		level++
		if level >= top {
			total = 0
			break
		}
	}
	return tx.SetShipSkillExp(tx.ctx, db.SetShipSkillExpParams{Level: level, Exp: total, ShipID: shipID, SkillID: skillID})
}

// skillClass is a lesson as NavalAcademyProxy's Student reads it.
func skillClass(class db.CommanderSkillClass) *protobuf.SKILL_CLASS {
	return &protobuf.SKILL_CLASS{
		RoomId:     proto.Uint32(class.RoomID),
		ShipId:     proto.Uint32(class.ShipID),
		StartTime:  proto.Uint32(uint32(class.StartTime)),
		FinishTime: proto.Uint32(uint32(class.FinishTime)),
		SkillPos:   proto.Uint32(class.SkillID),
		Exp:        proto.Uint32(class.Exp),
	}
}

// quickFinishesUsed is how many lessons the commander finished instantly
// today.
func quickFinishesUsed(commander db.Commander, now time.Time) uint32 {
	if commander.QuickFinishDay != startOfDay(now).Unix() {
		return 0
	}
	return commander.QuickFinishes
}

// quickFinishesAllowed is how many lessons a day the buff activities the
// commander sees finish instantly: the globally open ones plus the ones that
// run on the commander's own clock (newUserActivities — Window.Open always
// refuses newuser activities), deduplicated by activity.
func quickFinishesAllowed(ctx context.Context, queries *db.Queries, commanderID uint32, now time.Time) (uint32, error) {
	active := map[uint32]bool{}
	for _, config := range openActivities(now) {
		active[config.ID] = true
	}
	own, err := commanderActivities(ctx, queries, commanderID, now)
	if err != nil {
		return 0, err
	}
	for id := range own {
		active[id] = true
	}
	var n uint32
	for id := range active {
		config, ok := activityConfigs[id]
		if !ok || config.Type != activityTypeBuff {
			continue
		}
		buffs := []uint32{config.ConfigID}
		if config.ConfigID == 0 {
			buffs = gamedata.IDs(config.ConfigData)
		}
		for _, buff := range buffs {
			n += skillLearnBuffs[buff]
		}
	}
	return n, nil
}

// navalAcademy is SC_22001, NavalAcademyProxy's state: the levels of the
// oil field, merchant and lecture hall (their template tables start at 1,
// captured) and when their running upgrade ends (0 none, captured; the
// client counts down to it and levels the field up itself, an end it
// already passed would leave the field unable to upgrade), the hall's
// proficiency, the Tactical Class rooms, lessons and today's quick
// finishes. The fields are read again: SC_11003 settled them, finishing
// the upgrades that ended, after the login loaded the commander.
func navalAcademy(ctx context.Context, queries *db.Queries, commander db.Commander, now time.Time) (*protobuf.SC_22001, error) {
	classes, err := queries.ListSkillClasses(ctx, commander.ID)
	if err != nil {
		return nil, err
	}
	fields, err := queries.GetCommander(ctx, commander.ID)
	if err != nil {
		return nil, err
	}
	msg := &protobuf.SC_22001{
		OilWellLevel:       proto.Uint32(fields.OilFieldLevel),
		OilWellLvUpTime:    proto.Uint32(uint32(fields.OilFieldUpgradeAt)),
		GoldWellLevel:      proto.Uint32(fields.GoldFieldLevel),
		GoldWellLvUpTime:   proto.Uint32(uint32(fields.GoldFieldUpgradeAt)),
		ClassLv:            proto.Uint32(fields.ClassFieldLevel),
		ClassLvUpTime:      proto.Uint32(uint32(fields.ClassFieldUpgradeAt)),
		Class:              &protobuf.NAVALACADEMY_CLASS{Proficiency: proto.Uint32(fields.ClassProficiency)},
		SkillClassList:     make([]*protobuf.SKILL_CLASS, len(classes)),
		SkillClassNum:      proto.Uint32(commander.SkillClassNum),
		DailyFinishBuffCnt: proto.Uint32(quickFinishesUsed(commander, now)),
	}
	for i, class := range classes {
		msg.SkillClassList[i] = skillClass(class)
	}
	return msg, nil
}

// handleStartLesson starts a lesson (StartLearnTacticsCommand, CS_22201):
// ship_id studies its skill skill_pos (a skill id, Student:getSkillId) in
// room_id with the book item_id. On result 0 the client makes a Student of
// class_info, its time the book's usage_arg[1], and takes the book from its
// bag itself; it tells lesson_endToLearn "Class failed: " otherwise.
func (game *Game) handleStartLesson(client *connection.Client, req *protobuf.CS_22201) error {
	ctx := context.Background()
	var class db.CommanderSkillClass
	err := game.tx(ctx, client, func(tx *Tx) error {
		commander, err := tx.GetCommander(ctx, client.CommanderID)
		if err != nil {
			return err
		}
		book, ok := lessonBooks[req.GetItemId()]
		if !ok || req.GetRoomId() < 1 || req.GetRoomId() > commander.SkillClassNum {
			return errRefused
		}
		// one lesson per room, one room per ship
		classes, err := tx.ListSkillClasses(ctx, client.CommanderID)
		if err != nil {
			return err
		}
		for _, class := range classes {
			if class.RoomID == req.GetRoomId() || class.ShipID == req.GetShipId() {
				return errRefused
			}
		}
		skill, err := tx.GetShipSkill(ctx, db.GetShipSkillParams{CommanderID: client.CommanderID, ShipID: req.GetShipId(), SkillID: req.GetSkillPos()})
		if errors.Is(err, sql.ErrNoRows) {
			return errRefused
		} else if err != nil {
			return err
		}
		// the client says tactics_max_level itself
		if skill.Level >= skillMaxLevel(skill.SkillID) {
			return errRefused
		}
		if err := tx.SpendItem(req.GetItemId(), 1); err != nil {
			return err
		}
		now := time.Now().Unix()
		class = db.CommanderSkillClass{
			CommanderID: client.CommanderID,
			RoomID:      req.GetRoomId(),
			ShipID:      req.GetShipId(),
			SkillID:     skill.SkillID,
			ItemID:      req.GetItemId(),
			StartTime:   now,
			FinishTime:  now + int64(book.time),
			Exp:         lessonExp(book, skill.SkillID),
		}
		return tx.AddSkillClass(ctx, db.AddSkillClassParams(class))
	})
	if err != nil && !refused(err) {
		return err
	}
	reply := &protobuf.SC_22202{Result: result(err)}
	if err == nil {
		reply.ClassInfo = skillClass(class)
	}
	return client.Send(reply)
}

// endLesson ends the lesson in room, its skill getting the share of the
// lesson's exp its time in class earned (all of it once finished), and
// returns that exp.
func (tx *Tx) endLesson(room uint32, now time.Time) (uint32, error) {
	class, err := tx.GetSkillClass(tx.ctx, db.GetSkillClassParams{CommanderID: tx.commanderID, RoomID: room})
	if errors.Is(err, sql.ErrNoRows) {
		return 0, errRefused
	} else if err != nil {
		return 0, err
	}
	exp := class.Exp
	if elapsed, length := now.Unix()-class.StartTime, class.FinishTime-class.StartTime; elapsed < length {
		exp = uint32(int64(class.Exp) * max(elapsed, 0) / length)
	}
	if err := tx.addSkillExp(class.ShipID, class.SkillID, exp); err != nil {
		return 0, err
	}
	return exp, tx.DeleteSkillClass(tx.ctx, db.DeleteSkillClassParams{CommanderID: tx.commanderID, RoomID: room})
}

// handleEndLesson ends the lesson in room_id (CancelLearnTacticsCommand,
// CS_22203): type cancelTypeAuto once its time is up, cancelTypeManual to
// stop it early. On result 0 the client adds exp to the skill
// (Ship:addSkillExp) and frees the room. The official server's share of the
// exp for a lesson stopped early isn't known; belfast gives the share of
// its time spent in class.
func (game *Game) handleEndLesson(client *connection.Client, req *protobuf.CS_22203) error {
	var exp uint32
	err := game.tx(context.Background(), client, func(tx *Tx) error {
		if req.GetType() != cancelTypeAuto && req.GetType() != cancelTypeManual {
			return errRefused
		}
		var err error
		exp, err = tx.endLesson(req.GetRoomId(), time.Now())
		return err
	})
	if err != nil && !refused(err) {
		return err
	}
	return client.Send(&protobuf.SC_22204{Result: result(err), Exp: proto.Uint32(exp)})
}

// handleQuickFinishLesson finishes the lesson in roomid now
// (QuickFinsihLearnTacticsCommand, CS_22014), one of the
// quickFinishesAllowed of the day. On result 0 the client counts it
// (updateUsedDailyFinishCnt) and adds the lesson's whole exp to the skill;
// it tells lesson_quickfinsh otherwise.
func (game *Game) handleQuickFinishLesson(client *connection.Client, req *protobuf.CS_22014) error {
	ctx := context.Background()
	err := game.tx(ctx, client, func(tx *Tx) error {
		commander, err := tx.GetCommander(ctx, client.CommanderID)
		if err != nil {
			return err
		}
		now := time.Now()
		used := quickFinishesUsed(commander, now)
		allowed, err := quickFinishesAllowed(ctx, tx.Queries, commander.ID, now)
		if err != nil {
			return err
		}
		if used >= allowed {
			return errRefused
		}
		class, err := tx.GetSkillClass(ctx, db.GetSkillClassParams{CommanderID: client.CommanderID, RoomID: req.GetRoomid()})
		if errors.Is(err, sql.ErrNoRows) {
			return errRefused
		} else if err != nil {
			return err
		}
		if _, err := tx.endLesson(class.RoomID, time.Unix(class.FinishTime, 0)); err != nil {
			return err
		}
		return tx.SetQuickFinishes(ctx, db.SetQuickFinishesParams{
			QuickFinishDay: startOfDay(now).Unix(),
			QuickFinishes:  used + 1,
			ID:             client.CommanderID,
		})
	})
	if err != nil && !refused(err) {
		return err
	}
	return client.Send(&protobuf.SC_22015{Result: result(err)})
}
