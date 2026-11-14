-- The second ID list of an ACTIVITYINFO (data2_list): the stages an event's
-- stage page has cleared (ACTIVITY_TYPE_BOSSSINGLE, e.g. the TwoPointFive
-- page's 2079001-2079003), which the client reads as Activity.data2_list to
-- unlock the next stage. The client appends them itself on the operation's
-- success, so the server has to keep them for the next login.
ALTER TABLE commander_activities ADD COLUMN data2_list TEXT NOT NULL DEFAULT '[]';
