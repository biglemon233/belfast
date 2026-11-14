-- name: ListActivities :many
SELECT * FROM commander_activities WHERE commander_id = ?;

-- name: GetActivity :one
SELECT * FROM commander_activities WHERE commander_id = ? AND activity_id = ?;

-- name: SaveActivity :exec
INSERT INTO commander_activities (commander_id, activity_id, data1, data2, data3, data4, data1_list, data2_list, str_data1) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (commander_id, activity_id) DO UPDATE SET
	data1 = excluded.data1, data2 = excluded.data2, data3 = excluded.data3, data4 = excluded.data4, data1_list = excluded.data1_list, data2_list = excluded.data2_list, str_data1 = excluded.str_data1;
