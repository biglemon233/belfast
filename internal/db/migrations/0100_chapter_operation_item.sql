-- the operation item (高效作战指令书, item_data_statistics 61001) a sortie was
-- started with, 0 for none: its oil and morale cost and its battle gains all
-- double (the item's own display says so)
ALTER TABLE chapter_states ADD COLUMN operation_item INTEGER NOT NULL DEFAULT 0;
