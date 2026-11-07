// Package game answers the game server packets sent once a commander is
// logged in, starting with the player data load (CS_11001).
package game

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ggmolly/belfast/internal/connection"
	"github.com/ggmolly/belfast/internal/db"
	"github.com/ggmolly/belfast/internal/live"
	"github.com/ggmolly/belfast/internal/packets"
	"github.com/ggmolly/belfast/internal/protobuf"
)

type Game struct {
	DB *db.Store
	// Live gets an event for each save written and each login or logout,
	// for the back-office's open pages (nil: none)
	Live *live.Broker

	online online // the commanders playing, see connect
	// who entered their secondary password since logging in
	secondaryUnlocks secondaryUnlocks
	// the auction minigame's matchmaking and matches, see auction_match.go
	auctions auctionHall
	// the play room lobby and its games, see playroom.go
	playRooms playRooms
	// who is on which island (the life-sim), see island_enter.go
	islands islandWorld
}

// StartCommander creates what a new commander starts with (CS_10024), in
// dependency order: ships first, the secretary and fleets point at them.
// What a new commander starts with is no progress: the task events and
// pushes its Tx records are dropped.
func StartCommander(ctx context.Context, queries *db.Queries, commander db.Commander) error {
	tx := newTx(ctx, queries, commander.ID)
	for _, grant := range []func(*Tx, db.Commander) error{
		grantShipsStarter,
		grantPlayerStarter,
		grantFleetsStarter,
		grantTasksStarter,
	} {
		if err := grant(tx, commander); err != nil {
			return err
		}
	}
	return nil
}

// LoadGameData reads the config tables the game server uses, for what runs
// without Register (the mail command).
func LoadGameData() error {
	return loadGameData()
}

// Register loads the game data and installs the game server packet handlers.
// It panics when the game data can't be loaded.
func (game *Game) Register() {
	if err := loadGameData(); err != nil {
		panic(fmt.Sprintf("game data: %v", err))
	}
	// the player, settings and guild
	packets.Handle(game.handleLoadPlayerData)
	// the backyard
	packets.Handle(game.handleDormPopEvents)
	packets.Handle(game.handleBackyardVisitor)
	packets.Handle(game.handleDormShipExp)
	packets.Handle(game.handleDormAddShip)
	packets.Handle(game.handleDormShipExit)
	packets.Handle(game.handleDormIntimacy)
	packets.Handle(game.handleDormMoney)
	packets.Handle(game.handlePutFurniture)
	packets.Handle(game.handleDormRename)
	packets.Handle(game.handleBuyFurniture)
	packets.Handle(game.handleThemeTemplates)
	packets.Handle(game.handleSaveThemeTemplate)
	packets.Handle(game.handleDeleteThemeTemplate)
	packets.Handle(game.handleShareThemeTemplate)
	packets.Handle(game.handleUnshareThemeTemplate)
	packets.Handle(game.handleThemeShop)
	packets.Handle(game.handleThemeTemplate)
	packets.Handle(game.handleThemePreviews)
	packets.Handle(game.handleLikeThemeTemplate)
	packets.Handle(game.handleCollectThemeTemplate)
	packets.Handle(game.handleUncollectThemeTemplate)
	packets.Handle(game.handleCollectedThemes)
	packets.Handle(game.handleReportThemeTemplate)
	packets.Handle(game.handleThemeStorage)
	packets.Handle(game.handleDormThemes)
	packets.Handle(game.handleSaveDormTheme)
	packets.Handle(game.handleDeleteDormTheme)
	packets.Handle(game.handleVisitBackyard)
	packets.Handle(game.handleGuideIndex)
	packets.Handle(game.handleStoryRead)
	packets.Handle(game.handleCommonFlag)
	packets.Handle(game.handleHarvestResource)
	packets.Handle(game.handleHarvestClassResource)
	packets.Handle(game.handleSecondaryPassword)
	packets.Handle(game.handleSetSecondaryPassword)
	packets.Handle(game.handleSecondaryPasswordSettings)
	packets.Handle(game.handleConfirmSecondaryPassword)
	packets.Handle(game.handleAttireApply)
	packets.Handle(game.handleCancelCommonFlag)
	packets.Handle(game.handleChangeCover)
	packets.Handle(game.handleStoryList)
	packets.Handle(game.handleLoadingPics)
	packets.Handle(game.handleShipCount)
	packets.Handle(game.handleGMCommand)
	packets.Handle(game.handleChangeChatRoom)
	packets.Handle(game.handleClientReport)
	// Operation Siren: state, activation, maps, stamina
	packets.Handle(game.handleWorldState)
	packets.Handle(game.handleWorldActivate)
	packets.Handle(game.handleWorldMapReq)
	packets.Handle(game.handleWorldStaminaExchange)
	packets.Handle(game.handleWorldKill)
	// Operation Siren: map ops
	packets.Handle(game.handleWorldMapOp)
	// Operation Siren: ports and items
	packets.Handle(game.handleWorldPortReq)
	packets.Handle(game.handleWorldPortShopping)
	packets.Handle(game.handleWorldShipRepair)
	packets.Handle(game.handleWorldFleetRedeploy)
	packets.Handle(game.handleWorldItemUse)
	// Operation Siren: tasks and achievements
	packets.Handle(game.handleWorldTriggerTask)
	packets.Handle(game.handleWorldSubmitTask)
	packets.Handle(game.handleWorldDailyTasks)
	packets.Handle(game.handleWorldTriggerDailyTask)
	packets.Handle(game.handleWorldAchieve)
	// META Showdown (world boss) and META Sync Points
	packets.Handle(game.handleMetaPTData)
	packets.Handle(game.handleMetaPTAward)
	packets.Handle(game.handleWorldBoss)
	packets.Handle(game.handleWorldBossRank)
	packets.Handle(game.handleWorldBossSupport)
	packets.Handle(game.handleWorldBossAward)
	packets.Handle(game.handleWorldBossOvertime)
	packets.Handle(game.handleCheckWorldBoss)
	packets.Handle(game.handleCacheBossHP)
	packets.Handle(game.handleWorldBossFormation)
	packets.Handle(game.handleActiveWorldBoss)
	packets.Handle(game.handleWorldBossAutoBattle)
	packets.Handle(game.handleWorldBossStopAutoBattle)
	packets.Handle(game.handleSwitchWorldBossArchives)
	packets.Handle(game.handleMetaTacticsInfo)
	packets.Handle(game.handleMetaTactics)
	packets.Handle(game.handleMetaTacticsUnlock)
	packets.Handle(game.handleMetaTacticsSwitch)
	packets.Handle(game.handleMetaTacticsLevelUp)
	packets.Handle(game.handleMetaQuickTactics)
	packets.Handle(game.handleMetaRepair)
	packets.Handle(game.handleMetaEnergy)
	packets.Handle(game.handleStartLesson)
	packets.Handle(game.handleEndLesson)
	packets.Handle(game.handleQuickFinishLesson)
	// Research Academy projects
	packets.Handle(game.handleStartResearch)
	packets.Handle(game.handleFinishResearch)
	packets.Handle(game.handleStopResearch)
	packets.Handle(game.handleRefreshResearch)
	packets.Handle(game.handleResearchFocus)
	packets.Handle(game.handleResearchCatchup)
	packets.Handle(game.handleJoinResearchQueue)
	packets.Handle(game.handleFinishResearchQueue)
	// Fleet Technology
	packets.Handle(game.handleStartFleetTech)
	packets.Handle(game.handleFinishFleetTech)
	packets.Handle(game.handleFleetTechAward)
	packets.Handle(game.handleFleetTechAwardAll)
	packets.HandleLenient(game.handleSetFleetTechAttrs)
	packets.Handle(game.handleGuildInfo)
	packets.Handle(game.handleCreateGuild)
	packets.Handle(game.handleGuildRequests)
	packets.Handle(game.handleGuildApply)
	packets.Handle(game.handleDissolveGuild)
	packets.Handle(game.handleSetGuildDuty)
	packets.Handle(game.handleFireGuildMember)
	packets.Handle(game.handleImpeachGuildCommander)
	packets.Handle(game.handleQuitGuild)
	packets.Handle(game.handleAcceptGuildRequest)
	packets.Handle(game.handleRejectGuildRequest)
	packets.Handle(game.handleGuildList)
	packets.Handle(game.handleModifyGuild)
	packets.Handle(game.handleSearchGuild)
	packets.Handle(game.handlePublicGuildTech)
	packets.Handle(game.handleUpgradeGuildTech)
	packets.Handle(game.handleStartGuildTechGroup)
	packets.Handle(game.handleGuildUserInfo)
	packets.Handle(game.handleGuildDonate)
	packets.Handle(game.handleGuildBuySupply)
	packets.Handle(game.handleGuildSupplyAward)
	packets.Handle(game.handleGuildCapitalLog)
	packets.Handle(game.handleGuildCapital)
	packets.Handle(game.handleGuildSelectWeeklyTask)
	packets.Handle(game.handleGuildWeeklyTaskProgress)
	packets.Handle(game.handleGuildRank)
	packets.Handle(game.handleTriggerTask)
	packets.Handle(game.handleGuildShop)
	packets.Handle(game.handleGuildShopPurchase)
	packets.Handle(game.handleGuildSendMsg)
	packets.Handle(game.handleGuildChatList)
	packets.Handle(game.handleGuildUpdateAssaultFleet)
	packets.Handle(game.handleMyGuildAssaultFleet)
	packets.Handle(game.handleGuildAssaultFleets)
	packets.Handle(game.handleGuildRecommendAssaultShip)
	packets.Handle(game.handleGuildRecommendedShips)
	// guild operations
	packets.Handle(game.handleActivateGuildOperation)
	packets.Handle(game.handleGuildOperation)
	packets.Handle(game.handleGuildDispatch)
	packets.Handle(game.handleGuildBossFleets)
	packets.Handle(game.handleGuildOperation61015)
	packets.Handle(game.handleGuildReports)
	packets.Handle(game.handleSubmitGuildReports)
	packets.Handle(game.handleRefreshGuildMission)
	packets.Handle(game.handleGuildNodeAnimation)
	packets.Handle(game.handleGuildBossInfo)
	packets.Handle(game.handleGuildBossRank)
	packets.Handle(game.handleJoinGuildOperation)
	packets.Handle(game.handleGuildReportRank)
	// ships, builds and inventory
	packets.Handle(game.handleBuildShip)
	packets.Handle(game.handleBuildQuickFinish)
	packets.Handle(game.handleBuildResults)
	packets.Handle(game.handleCollectBuilds)
	packets.Handle(game.handleConfirmBuilds)
	packets.Handle(game.handleBuildExchange)
	packets.Handle(game.handleShipLock)
	packets.Handle(game.handleSetShipSkin)
	packets.Handle(game.handleShipEquip)
	packets.Handle(game.handleShipEquipSkin)
	packets.Handle(game.handleRetireShips)
	packets.Handle(game.handleLoveLetters)
	packets.Handle(game.handlePropose)
	packets.Handle(game.handleRenameShip)
	packets.Handle(game.handleShipPreference)
	packets.Handle(game.handleReqPlayerAssist)
	packets.Handle(game.handleUnlockLoveLetter)
	packets.Handle(game.handleLoveLetterLevelReward)
	packets.Handle(game.handleRealizeLoveLetterGift)
	packets.Handle(game.handleLoveLetterLevelUp)
	packets.Handle(game.handleLoveLetterContent)
	packets.Handle(game.handleUpgradeStar)
	packets.Handle(game.handleUpgradeMaxLevel)
	packets.Handle(game.handleModShip)
	packets.Handle(game.handleRemouldShip)
	packets.Handle(game.handleCollectionAward)
	packets.Handle(game.handleTrophyClaim)
	packets.Handle(game.handleChangeMedalDisplay)
	packets.Handle(game.handleUpgradeShipEquipment)
	packets.Handle(game.handleUpgradeEquipment)
	packets.Handle(game.handleComposeEquipment)
	packets.Handle(game.handleDestroyEquipments)
	packets.Handle(game.handleShipSpWeapon)
	packets.Handle(game.handleUpgradeSpWeapon)
	packets.Handle(game.handleReforgeSpWeapon)
	packets.Handle(game.handleConfirmReforgeSpWeapon)
	packets.Handle(game.handleCompositeSpWeapon)
	packets.Handle(game.handleRevertEquipment)
	packets.Handle(game.handleTransformShipEquipment)
	packets.Handle(game.handleTransformEquipment)
	packets.HandleLenient(game.handleSellItems)
	packets.Handle(game.handleUseItem)
	packets.Handle(game.handleQuickExchangeBlueprint)
	packets.Handle(game.handleGetPhantomQuestProgress)
	packets.Handle(game.handleFinishPhantomQuest)
	// research ships (Dev. Dock)
	packets.Handle(game.handleStartBlueprint)
	packets.Handle(game.handleFinishBlueprint)
	packets.Handle(game.handleModBlueprint)
	packets.Handle(game.handleStopBlueprint)
	packets.Handle(game.handleBlueprintSpeedup)
	packets.Handle(game.handlePursueBlueprint)
	packets.Handle(game.handleItemUnlockBlueprint)
	packets.Handle(game.handleUseShipExpBooks)
	packets.Handle(game.handleExchangeRing)
	packets.Handle(game.handleComposeItem)
	packets.Handle(game.handleTracking)
	packets.Handle(game.handleTrack)
	packets.Handle(game.handleNewTracking)
	packets.Handle(game.handleMainSceneTracking)
	packets.Handle(game.handleUrExchangeTracking)
	// fleets, the campaign and battles
	packets.Handle(game.handleUpdateFleet)
	packets.Handle(game.handleRenameFleet)
	packets.Handle(game.handleTracking13101)
	packets.Handle(game.handleUpdateCustomFleet)
	packets.Handle(game.handleRemoveEliteTargetShip)
	packets.Handle(game.handleChapterDropShips)
	packets.Handle(game.handleChapterOp)
	packets.Handle(game.handleChapterBattleResult)
	packets.Handle(game.handleRemasterInfo)
	packets.Handle(game.handleRemasterTickets)
	packets.Handle(game.handleRemasterAward)
	packets.Handle(game.handleBeginStage)
	packets.Handle(game.handleFinishStage)
	packets.Handle(game.handleQuitBattle)
	packets.Handle(game.handleDailyQuickBattle)
	packets.Handle(game.handleStartChapterAuto)
	packets.Handle(game.handleStartWorldChapterAuto)
	packets.Handle(game.handleEndChapterAuto)
	packets.Handle(game.handleAddChapterAutoTime)
	packets.Handle(game.handleStartCommission)
	packets.Handle(game.handleFinishCommission)
	packets.Handle(game.handleGiveUpCommission)
	packets.Handle(game.handleFlushCommissions)
	// tasks and activities
	packets.Handle(game.handleSubmitTask)
	packets.Handle(game.handleSubmitTasks)
	packets.Handle(game.handleSubmitWeeklyTask)
	packets.Handle(game.handleSubmitWeeklyTasks)
	packets.Handle(game.handleSubmitWeeklyProgress)
	packets.Handle(game.handleClientTaskEvent)
	packets.Handle(game.handleSubmitActivityTasks)
	packets.Handle(game.handleUpdateTaskProgress)
	packets.Handle(game.handleUpdateActivityTaskProgress)
	packets.Handle(game.handleQuickTask)
	packets.Handle(game.handleQuickActivityTask)
	packets.Handle(game.handleHandbookGetTasks)
	packets.Handle(game.handleHandbookPtAward)
	packets.Handle(game.handleActivityOperation)
	packets.Handle(game.handleEditActivityFleet)
	// mini-events, boss rush archives and Capsule Reruns
	packets.Handle(game.handlePermanentStart)
	packets.Handle(game.handlePermanentEnd)
	packets.Handle(game.handleRerunActivate)
	// returning player campaign
	packets.Handle(game.handleRefluxData)
	packets.Handle(game.handleRefluxSign)
	packets.Handle(game.handleRefluxPtAward)
	// server-wide activity bosses, the anniversary island
	packets.Handle(game.handleActivityBossPage)
	packets.Handle(game.handleCollabBossRushData)
	packets.Handle(game.handleIslandNodes)
	// the island's play rooms and the cheat bar
	packets.Handle(game.handlePlayRoomList)
	packets.Handle(game.handlePlayRoomInfo)
	packets.Handle(game.handlePlayRoomCreate)
	packets.Handle(game.handlePlayRoomJoin)
	packets.Handle(game.handlePlayRoomSwitchViewer)
	packets.Handle(game.handlePlayRoomExit)
	packets.Handle(game.handlePlayRoomKick)
	packets.Handle(game.handlePlayRoomReady)
	packets.Handle(game.handlePlayRoomStart)
	packets.Handle(game.handlePlayRoomMatchReady)
	packets.Handle(game.handlePlayRoomInvite)
	packets.Handle(game.handlePlayRoomChat)
	packets.Handle(game.handlePlayRoomRank)
	packets.Handle(game.handlePlayRoomLoad)
	packets.Handle(game.handlePlayRoomView)
	packets.Handle(game.handleCheatBarOperate)
	packets.Handle(game.handleCheatBarReconnect)
	packets.Handle(game.handleCheatBarCancelDelegate)
	// coloring activity
	packets.Handle(game.handleColoringFetch)
	packets.Handle(game.handleColoringCell)
	packets.Handle(game.handleColoringClear)
	packets.Handle(game.handleColoringAchieve)
	// new commander and Black Friday shops, stored activity data,
	// anniversary summary
	packets.Handle(game.handleGetNewServerShop)
	packets.Handle(game.handleNewServerShopShopping)
	packets.Handle(game.handleActivityStoreData)
	packets.Handle(game.handleGetSummaryInfo)
	// challenge mode
	packets.Handle(game.handleChallengeStart)
	packets.Handle(game.handleChallengeInfo)
	packets.Handle(game.handleChallengeReset)
	// Juus
	packets.Handle(game.handleJuusPostOp)
	packets.Handle(game.handleJuusComment)
	packets.Handle(game.handleJuusPosts)
	packets.Handle(game.handleJuusGroups)
	packets.Handle(game.handleJuusChatReply)
	packets.Handle(game.handleJuusTopicsRead)
	packets.Handle(game.handleJuusActivateTopics)
	packets.Handle(game.handleJuusSkin)
	packets.Handle(game.handleJuusCare)
	packets.Handle(game.handleJuusTopic)
	// auction minigame (AuctionGameProxy, AuctionGameBaseProxy)
	packets.Handle(game.handleAuctionGameInit)
	packets.Handle(game.handleAuctionQuickMatch)
	packets.Handle(game.handleAuctionMatchingReconnect)
	packets.Handle(game.handleAuctionEventSelected)
	packets.Handle(game.handleAuctionBid)
	packets.Handle(game.handleAuctionForfeit)
	packets.Handle(game.handleAuctionEmoji)
	packets.Handle(game.handleAuctionSwitchEmoji)
	packets.Handle(game.handleAuctionGamePreorderBox)
	packets.Handle(game.handleAuctionGameOpenBox)
	packets.Handle(game.handleAuctionGameShowMatchWarningTip)
	packets.Handle(game.handleAuctionGameGetRelief)
	// minigames, shops, exercises, TB
	packets.Handle(game.handleAppreciationVersion)
	packets.Handle(game.handleGalleryUnlock)
	packets.Handle(game.handleMusicUnlock)
	packets.Handle(game.handleGalleryLike)
	packets.Handle(game.handleMusicLike)
	packets.Handle(game.handleMangaRead)
	packets.Handle(game.handleMangaLike)
	packets.Handle(game.handleMusicPlayer)
	packets.Handle(game.handleMiniGameHubs)
	packets.Handle(game.handleMiniGameOperation)
	packets.Handle(game.handleMiniGameFriendRank)
	packets.Handle(game.handleGameRoomFirstCoin)
	packets.Handle(game.handleGameRoomWeekCoin)
	packets.Handle(game.handleGameRoomExchangeCoin)
	packets.Handle(game.handleGameRoomSuccess)
	packets.Handle(game.handleAtelierRequest)
	packets.Handle(game.handleAtelierComposite)
	packets.Handle(game.handleAtelierRefreshBuff)
	// City Rebuild activities (CityRebuildProxy)
	packets.Handle(game.handleCityRebuildData)
	packets.Handle(game.handleCityRebuildRecruited)
	packets.Handle(game.handleCityRebuildBuild)
	packets.Handle(game.handleCityRebuildBuff)
	packets.Handle(game.handleCityRebuildResult)
	packets.Handle(game.handleCityRebuildLevel)
	packets.Handle(game.handleCityRebuildStart)
	packets.Handle(game.handleFeastData)
	packets.Handle(game.handleFeastRandomShips)
	packets.Handle(game.handleEmojiInfo)
	packets.Handle(game.handleLimitChallenge)
	packets.Handle(game.handleLimitChallengeAward)
	packets.Handle(game.handleChargeList)
	// purchases with money (always refused), Akashi, redeem codes, surveys
	packets.Handle(game.handleCharge)
	packets.Handle(game.handleRefundCharge)
	packets.Handle(game.handleChargeConfirm)
	packets.Handle(game.handleChargeFailed)
	packets.Handle(game.handleRefundInfo)
	packets.Handle(game.handleClickMingShi)
	packets.Handle(game.handleExchangeCode)
	packets.Handle(game.handleSurvey)
	packets.Handle(game.handleSurveyState)
	packets.Handle(game.handleShopping)
	packets.Handle(game.handleChangeSecretaries)
	packets.Handle(game.handleRandomFlagship)
	packets.Handle(game.handleChangeRandomFlagshipMode)
	packets.Handle(game.handleChangeRandomFlagships)
	packets.Handle(game.handleChangeName)
	packets.Handle(game.handleChangeManifesto)
	packets.Handle(game.handleMedalShop)
	packets.Handle(game.handleMedalShopPurchase)
	packets.Handle(game.handleRequisition)
	packets.Handle(game.handleMonthShopping)
	packets.Handle(game.handleUnlockCryptolalia)
	packets.Handle(game.handleShopStreet)
	packets.Handle(game.handleMiniGameShop)
	packets.Handle(game.handleMiniGameShopBuy)
	packets.Handle(game.handleMiniGameShopFlush)
	packets.Handle(game.handleSeasonInfo)
	packets.Handle(game.handleReplaceRivals)
	packets.Handle(game.handleArenaRank)
	packets.Handle(game.handleExerciseFleet)
	packets.Handle(game.handleRivalInfo)
	packets.Handle(game.handleMilitaryShop)
	packets.Handle(game.handleRefreshMilitaryShop)
	packets.Handle(game.handleRankPage)
	packets.Handle(game.handleRankSelf)
	packets.Handle(game.handleSkinPreviewUsage)
	packets.Handle(game.handleEducate)
	packets.Handle(game.handleNewEducate)
	// Project Identity (neweducate): Oceana, Explorer
	packets.Handle(game.handleNewEducateGetEndings)
	packets.Handle(game.handleNewEducateSelEnding)
	packets.Handle(game.handleNewEducateReset)
	packets.Handle(game.handleNewEducateRefresh)
	packets.Handle(game.handleNewEducateSetCall)
	packets.Handle(game.handleNewEducateMainEvent)
	packets.Handle(game.handleNewEducateAssess)
	packets.Handle(game.handleNewEducateGetTopics)
	packets.Handle(game.handleNewEducateSelTopic)
	packets.Handle(game.handleNewEducateGetTalents)
	packets.Handle(game.handleNewEducateRefreshTalent)
	packets.Handle(game.handleNewEducateSelTalent)
	packets.Handle(game.handleNewEducateChangePhase)
	packets.Handle(game.handleNewEducateUpgradeFavor)
	packets.Handle(game.handleNewEducateTriggerNode)
	packets.Handle(game.handleNewEducateClearNodeChain)
	packets.Handle(game.handleNewEducateSchedule)
	packets.Handle(game.handleNewEducateNextPlan)
	packets.Handle(game.handleNewEducateUpgradePlan)
	packets.Handle(game.handleNewEducateScheduleSkip)
	packets.Handle(game.handleNewEducateGetExtraDrop)
	packets.Handle(game.handleNewEducateEnterAssess)
	packets.Handle(game.handleNewEducateGetMap)
	packets.Handle(game.handleNewEducateMapNormal)
	packets.Handle(game.handleNewEducateMapEvent)
	packets.Handle(game.handleNewEducateShopping)
	packets.Handle(game.handleNewEducateMapShip)
	packets.Handle(game.handleNewEducateUpgradeNormalSite)
	packets.Handle(game.handleNewEducateRefreshShop)
	packets.Handle(game.handleNewEducateSelMind)
	packets.Handle(game.handleNewEducateGiveUpChoice)
	packets.Handle(game.handleNewEducateMakeChoice)
	packets.Handle(game.handleNewEducateRefreshChoice)
	packets.Handle(game.handleNewEducateRequestChoices)
	packets.Handle(game.handleNewEducateReplaceTarot)
	packets.Handle(game.handleNewEducateUpgradeEntry)
	packets.Handle(game.handleNewEducateGiveUpEntryUp)
	packets.Handle(game.handleNewEducateGetChoose)
	packets.Handle(game.handleEducateEndings)
	// the Island life-sim (protocol module 21), see island_enter.go
	game.registerIsland()
	// Project Identity: TB (educate)
	packets.HandleLenient(game.handleEducatePlans) // an empty schedule is sent as one byte
	packets.Handle(game.handleEducateRunPlans)
	packets.Handle(game.handleEducateHomeEvents)
	packets.Handle(game.handleEducateHomeEvent)
	packets.Handle(game.handleEducateSpecialEvent)
	packets.Handle(game.handleEducateRandomOptions)
	packets.Handle(game.handleEducateSiteOption)
	packets.Handle(game.handleEducateShopData)
	packets.Handle(game.handleEducateShopping)
	packets.Handle(game.handleEducateSetTarget)
	packets.Handle(game.handleEducateSubmitTask)
	packets.Handle(game.handleEducateTargetAward)
	packets.Handle(game.handleEducateTaskProgress)
	packets.Handle(game.handleEducateUpgradeFavor)
	packets.Handle(game.handleEducateExtraAttr)
	packets.Handle(game.handleEducateSetCall)
	packets.Handle(game.handleEducateEnding)
	packets.Handle(game.handleEducateReset)
	packets.Handle(game.handleEducateRefresh)
	packets.Handle(game.handleEducateBuyCollect)
	packets.Handle(game.handleEducateCharacter)
	// Private Quarters (apartment): rooms, Intimacy, gifts, furniture
	packets.Handle(game.handleApartmentRoomUnlock)
	packets.Handle(game.handleApartmentTriggerFavor)
	packets.Handle(game.handleApartmentLevelUp)
	packets.Handle(game.handleApartmentReplaceFurniture)
	packets.Handle(game.handleApartmentGiveGift)
	packets.Handle(game.handleApartmentCollectItem)
	packets.Handle(game.handleApartmentChangeSkin)
	packets.Handle(game.handleApartmentTalk)
	packets.Handle(game.handleSelectDorm3dEnter)
	packets.Handle(game.handleApartmentInvite)
	packets.Handle(game.handleApartmentSetCall)
	packets.Handle(game.handleApartmentVisit)
	packets.Handle(game.handleApartmentHideParts)
	// Private Quarters (apartment): Juustagram, chats, phone calls
	packets.Handle(game.handleApartmentEvents)
	packets.Handle(game.handleApartmentInsMark)
	packets.Handle(game.handleApartmentInsReply)
	packets.Handle(game.handleApartmentChatBackground)
	packets.Handle(game.handleApartmentChatCare)
	packets.Handle(game.handleApartmentChatTopic)
	// meowfficers and the cattery
	packets.Handle(game.handleBuyMeowfficerBoxes)
	packets.Handle(game.handleTrainMeowfficerBox)
	packets.Handle(game.handleQuickFinishMeowfficerBoxes)
	packets.Handle(game.handleRefreshMeowfficerBoxes)
	packets.Handle(game.handleOpenMeowfficerBox)
	packets.Handle(game.handleEquipMeowfficer)
	packets.Handle(game.handleLockMeowfficer)
	packets.Handle(game.handleRenameMeowfficer)
	packets.Handle(game.handleFeedMeowfficers)
	packets.Handle(game.handleFetchMeowfficerTalents)
	packets.Handle(game.handleLearnMeowfficerTalent)
	packets.Handle(game.handleResetMeowfficerTalents)
	packets.Handle(game.handleCommanderHome)
	packets.Handle(game.handleCatteryScene)
	packets.Handle(game.handlePutMeowfficerInCattery)
	packets.Handle(game.handleCatteryOp)
	packets.Handle(game.handleChangeCatteryStyle)
	packets.Handle(game.handleSetMeowfficerPreset)
	packets.Handle(game.handleRenameMeowfficerPreset)
	// mail
	packets.Handle(game.handleMailList)
	packets.Handle(game.handleArchiveList)
	packets.Handle(game.handleMailOperation)
	packets.Handle(game.handleDeleteArchivedMail)
	packets.Handle(game.handleExtendMailStorage)
	packets.Handle(game.handleMailStorageWithdraw)
	packets.Handle(game.handleMailTitles)
	packets.Handle(game.handleLoveLetterMailCheck)
	packets.Handle(game.handleLoveLetterMailRepair)
	packets.Handle(game.handleCompensations)
	packets.Handle(game.handleClaimCompensation)
	// friends, world chat and reports
	packets.Handle(game.handleSearchPlayer)
	packets.Handle(game.handleRecommendPlayers)
	packets.Handle(game.handleBatchGetPlayers)
	packets.Handle(game.handleSendFriendRequest)
	packets.Handle(game.handleAcceptFriendRequest)
	packets.Handle(game.handleRejectFriendRequest)
	packets.Handle(game.handleDeleteFriend)
	packets.Handle(game.handleSendFriendMessage)
	packets.Handle(game.handleBlacklist)
	packets.Handle(game.handleBlockPlayer)
	packets.Handle(game.handleUnblockPlayer)
	packets.Handle(game.handleWorldChat)
	packets.Handle(game.handleReportPlayer)
	packets.Handle(game.handleThemeAuthor)
	// ship comments
	packets.Handle(game.handleFetchDiscussion)
	packets.Handle(game.handlePostDiscussion)
	packets.Handle(game.handleReviewDiscussion)
	packets.Handle(game.handleLikeShip)
	packets.Handle(game.handleReportDiscussion)
	packets.Handle(game.handleRequestEquipCodes)
	packets.Handle(game.handleShareEquipCode)
	packets.Handle(game.handleLikeEquipCode)
	packets.Handle(game.handleReportEquipCode)
	// popularity election
	packets.Handle(game.handleFetchVoteRank)
}

// login is what the login burst is built from, loaded once per CS_11001.
type login struct {
	ctx       context.Context
	queries   *db.Queries
	commander db.Commander
	ships     []db.Ship
	now       time.Time
}

// loginPush builds one push of the login burst.
type loginPush func(game *Game, l *login) (proto.Message, error)

// empty is a login push with nothing to report.
func empty[M any, PM interface {
	*M
	proto.Message
}]() loginPush {
	return func(*Game, *login) (proto.Message, error) {
		return PM(new(M)), nil
	}
}

// loginBurst is what the official server pushes for CS_11001, in its order
// (capture #010 to #049): everything before SC_11002 lands in the proxies the
// client just registered, SC_11002 then starts them. A push returning nil is
// one the region's client doesn't have.
var loginBurst = []loginPush{
	// PlayerProxy: server clock
	func(game *Game, l *login) (proto.Message, error) {
		return &protobuf.SC_11000{Timestamp: proto.Uint32(uint32(l.now.Unix())), Monday_0OclockTimestamp: proto.Uint32(uint32(mondayReference()))}, nil
	},
	func(game *Game, l *login) (proto.Message, error) { return game.playerInfo(l.ctx, l.commander, l.ships) },
	// PlayerProxy: no benefit buffs
	empty[protobuf.SC_11015](),
	// RefluxProxy: the returning player campaign
	func(game *Game, l *login) (proto.Message, error) {
		return refluxInfo(l.ctx, l.queries, l.commander.ID, l.now)
	},
	// SystemTipProxy (island delegation tips): nothing to collect
	empty[protobuf.SC_21536](),
	func(game *Game, l *login) (proto.Message, error) {
		return navalAcademy(l.ctx, l.queries, l.commander, l.now)
	},
	func(game *Game, l *login) (proto.Message, error) {
		return gameRoomInfo(l.ctx, l.queries, l.commander.ID, l.now)
	},
	// CommanderProxy: the meowfficers, cat boxes and saved pairs
	func(game *Game, l *login) (proto.Message, error) {
		return meowfficerList(l.ctx, l.queries, l.commander.ID, l.now)
	},
	func(game *Game, l *login) (proto.Message, error) {
		return collectionState(l.ctx, l.queries, l.commander.ID)
	},
	func(game *Game, l *login) (proto.Message, error) { return game.handbookLoginPush(l.ctx, l.commander) },
	func(game *Game, l *login) (proto.Message, error) {
		return buildDocksState(l.ctx, l.queries, l.commander.ID)
	},
	func(game *Game, l *login) (proto.Message, error) {
		return shipList(l.ctx, l.queries, l.commander.ID, l.ships)
	},
	func(game *Game, l *login) (proto.Message, error) { return fleetList(l.ctx, l.queries, l.commander.ID) },
	func(game *Game, l *login) (proto.Message, error) {
		return skinList(l.ctx, l.queries, l.commander.ID, l.now)
	},
	// TechnologyProxy: the research projects, queue and catch-up
	func(game *Game, l *login) (proto.Message, error) { return game.researchLoginPush(l) },
	// TechnologyProxy: the research ships (PR / DR)
	func(game *Game, l *login) (proto.Message, error) {
		return blueprintList(l.ctx, l.queries, l.commander.ID, l.now)
	},
	// TechnologyNationProxy: Fleet Technology
	func(game *Game, l *login) (proto.Message, error) {
		return fleetTechState(l.ctx, l.queries, l.commander.ID)
	},
	func(game *Game, l *login) (proto.Message, error) {
		msg, err := chapterList(l.ctx, l.queries, l.commander.ID, l.ships)
		if err != nil {
			return nil, err
		}
		return msg, fillChapterAuto(l.ctx, l.queries, l.commander.ID, l.now, msg)
	},
	func(game *Game, l *login) (proto.Message, error) {
		return currentChapter(l.ctx, l.queries, l.commander.ID)
	},
	func(game *Game, l *login) (proto.Message, error) {
		return commissionList(l.ctx, l.queries, l.commander.ID, l.commander.Level)
	},
	func(game *Game, l *login) (proto.Message, error) {
		return dailyLevelState(l.ctx, l.queries, l.commander.ID, l.now)
	},
	// ShopsProxy: the monthly shops
	func(game *Game, l *login) (proto.Message, error) {
		return monthShopInfo(l.ctx, l.queries, l.commander.ID, l.now)
	},
	// WorldProxy: Operation Siren's base state
	func(game *Game, l *login) (proto.Message, error) { return worldLoginState(l) },
	func(game *Game, l *login) (proto.Message, error) {
		return equipmentDepot(l.ctx, l.queries, l.commander.ID)
	},
	// EquipmentProxy: the gear skin box
	func(game *Game, l *login) (proto.Message, error) {
		return equipSkinBox(l.ctx, l.queries, l.commander.ID)
	},
	func(game *Game, l *login) (proto.Message, error) { return bag(l.ctx, l.queries, l.commander.ID) },
	func(game *Game, l *login) (proto.Message, error) { return taskList(l.ctx, l.queries, l.commander.ID) },
	func(game *Game, l *login) (proto.Message, error) {
		weekly, err := game.weeklyInfo(l.ctx, l.queries, l.commander.ID)
		return &protobuf.SC_20101{Info: weekly}, err
	},
	func(game *Game, l *login) (proto.Message, error) {
		return game.activityTasksList(l.ctx, l.queries, l.commander.ID, l.now)
	},
	func(game *Game, l *login) (proto.Message, error) {
		return game.dorm(l.ctx, l.queries, l.commander, l.ships)
	},
	func(game *Game, l *login) (proto.Message, error) { return energyTick(l.commander, l.now.Unix()), nil },
	// ApartmentProxy / Dorm3dInsProxy: the Private Quarters
	func(game *Game, l *login) (proto.Message, error) {
		return apartmentLogin(l.ctx, l.queries, l.commander.ID, l.now)
	},
	func(game *Game, l *login) (proto.Message, error) {
		return mailCounts(l.ctx, l.queries, l.commander.ID)
	},
	// CompensateProxy: the compensations left to claim
	func(game *Game, l *login) (proto.Message, error) {
		return compensationState(l.ctx, l.queries, l.commander.ID, l.now)
	},
	// FriendProxy / NotificationProxy: friends and requests waiting
	func(game *Game, l *login) (proto.Message, error) {
		return game.friendList(l.ctx, l.queries, l.commander.ID)
	},
	func(game *Game, l *login) (proto.Message, error) { return game.activityList(l.ctx, l.commander) },
	// ActivityPermanentProxy: the mini-events finished and running
	func(game *Game, l *login) (proto.Message, error) {
		return permanentState(l.ctx, l.queries, l.commander.ID)
	},
	// ActivityRemasterProxy: the Capsule Reruns picked, in the regions that
	// have them
	func(game *Game, l *login) (proto.Message, error) {
		if !reruns {
			return nil, nil
		}
		return rerunState(l.ctx, l.queries, l.commander.ID)
	},
	// ServerNoticeProxy: the bulletin board
	func(game *Game, l *login) (proto.Message, error) { return boardNotices(l.ctx, l.queries) },
	// PlayerProxy: everything is loaded, starts the proxies
	func(game *Game, l *login) (proto.Message, error) {
		return &protobuf.SC_11002{
			Timestamp:               proto.Uint32(uint32(l.now.Unix())),
			Monday_0OclockTimestamp: proto.Uint32(uint32(mondayReference())),
			ShipCount:               proto.Uint32(uint32(len(l.ships))),
		}, nil
	},
}

// handleLoadPlayerData answers CS_11001 (LoadPlayerDataCommand) with the
// login burst.
func (game *Game) handleLoadPlayerData(client *connection.Client, req *protobuf.CS_11001) error {
	if client.CommanderID == 0 {
		return fmt.Errorf("CS_11001 without a commander")
	}
	ctx := context.Background()
	commander, err := game.DB.GetCommander(ctx, client.CommanderID)
	if err != nil {
		return err
	}
	now := time.Now()
	if req.GetTimestamp() == 0 {
		// a login, not a reconnect (timestamp 1): the secondary password
		// is asked again
		game.setSecondaryUnlocked(commander.ID, false)
	}
	if err := expireSkins(ctx, game.DB, commander.ID, now); err != nil {
		return err
	}
	// the client never writes back the morale it recovered on its own
	if err := restoreMorale(ctx, game.DB.Queries, commander.ID); err != nil {
		return err
	}
	// the research ships' opening tasks first: syncTechPoints brings them
	// to the commander's points
	if err := game.syncBlueprintTasks(ctx, commander.ID, commander.Level); err != nil {
		return err
	}
	if err := game.syncTechPoints(ctx, commander.ID); err != nil {
		return err
	}
	// the manual's "what the commander already has" tasks, before its pages
	// are pushed
	if err := game.tx(ctx, client, func(tx *Tx) error { return tx.syncStateTasks() }); err != nil {
		return err
	}
	if _, err := game.resetWeekIfDue(ctx, commander.ID, now); err != nil {
		return err
	}
	// a weekly fixed task the commander reached the level of this week
	if _, err := acceptTasks(ctx, game.DB.Queries, commander.ID, weeklyFixedTasksAt(commander.Level), uint32(now.Unix())); err != nil {
		return err
	}
	ships, err := game.DB.ListShipsByCommander(ctx, commander.ID)
	if err != nil {
		return err
	}
	if err := game.startRefluxIfReturning(ctx, commander, len(ships), now); err != nil {
		return err
	}
	l := &login{ctx: ctx, queries: game.DB.Queries, commander: commander, ships: ships, now: now}
	for _, build := range loginBurst {
		push, err := build(game, l)
		if err != nil {
			return err
		}
		if push == nil {
			continue
		}
		if err := client.Send(push); err != nil {
			return err
		}
	}
	game.watchDays(client)
	return game.connect(ctx, client)
}
