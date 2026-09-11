package storage

func migrations() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS schema_migrations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			number INTEGER NOT NULL,
			name TEXT NOT NULL,
			applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS workshops (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			is_active INTEGER NOT NULL DEFAULT 1
		);`,
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			telegram_user_id INTEGER NOT NULL UNIQUE,
			telegram_username TEXT,
			display_name TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			is_active INTEGER NOT NULL DEFAULT 1
		);`,
		`CREATE TABLE IF NOT EXISTS workshop_members (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			workshop_id INTEGER NOT NULL,
			user_id INTEGER NOT NULL,
			role TEXT NOT NULL,
			is_active INTEGER NOT NULL DEFAULT 1,
			UNIQUE(workshop_id, user_id)
		);`,
		`CREATE TABLE IF NOT EXISTS telegram_chats (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			telegram_chat_id INTEGER NOT NULL UNIQUE,
			workshop_id INTEGER NOT NULL,
			title TEXT,
			is_active INTEGER NOT NULL DEFAULT 1
		);`,
		`CREATE TABLE IF NOT EXISTS materials (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			workshop_id INTEGER NOT NULL,
			name TEXT NOT NULL,
			category TEXT NOT NULL,
			base_unit TEXT NOT NULL,
			current_stock REAL NOT NULL DEFAULT 0,
			minimum_stock REAL NOT NULL DEFAULT 0,
			supplier TEXT,
			lead_time_days INTEGER NOT NULL DEFAULT 0,
			notes TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS products (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			workshop_id INTEGER NOT NULL,
			name TEXT NOT NULL,
			sku TEXT,
			product_type TEXT NOT NULL,
			current_stock REAL NOT NULL DEFAULT 0,
			minimum_stock REAL NOT NULL DEFAULT 0,
			notes TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS bom_items (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			workshop_id INTEGER NOT NULL,
			product_id INTEGER NOT NULL,
			component_type TEXT NOT NULL,
			material_id INTEGER,
			component_product_id INTEGER,
			quantity REAL NOT NULL DEFAULT 0,
			unit TEXT NOT NULL,
			technical_loss_percent REAL NOT NULL DEFAULT 0,
			notes TEXT
		);`,
		`CREATE TABLE IF NOT EXISTS inventory_movements (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			workshop_id INTEGER NOT NULL,
			material_id INTEGER NOT NULL,
			date TEXT,
			quantity REAL NOT NULL,
			movement_type TEXT NOT NULL,
			reference_type TEXT,
			reference_id INTEGER,
			user_id INTEGER,
			comment TEXT
		);`,
		`CREATE TABLE IF NOT EXISTS product_movements (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			workshop_id INTEGER NOT NULL,
			product_id INTEGER NOT NULL,
			date TEXT,
			quantity REAL NOT NULL,
			movement_type TEXT NOT NULL,
			reference_type TEXT,
			reference_id INTEGER,
			user_id INTEGER,
			comment TEXT
		);`,
		`CREATE TABLE IF NOT EXISTS production_records (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			workshop_id INTEGER NOT NULL,
			product_id INTEGER NOT NULL,
			date TEXT,
			attempted_quantity REAL NOT NULL,
			good_quantity REAL NOT NULL,
			scrap_quantity REAL NOT NULL,
			user_id INTEGER,
			notes TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS shipments (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			workshop_id INTEGER NOT NULL,
			product_id INTEGER NOT NULL,
			quantity REAL NOT NULL,
			channel TEXT,
			date TEXT,
			external_order_id TEXT,
			user_id INTEGER,
			notes TEXT
		);`,
		`CREATE TABLE IF NOT EXISTS production_plans (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			workshop_id INTEGER NOT NULL,
			product_id INTEGER NOT NULL,
			planned_quantity REAL NOT NULL,
			start_date TEXT,
			due_date TEXT,
			status TEXT NOT NULL,
			notes TEXT
		);`,
		`CREATE TABLE IF NOT EXISTS conversation_sessions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			workshop_id INTEGER NOT NULL,
			telegram_chat_id INTEGER NOT NULL,
			user_id INTEGER NOT NULL,
			last_context TEXT,
			pending_action TEXT,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS pending_actions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			workshop_id INTEGER NOT NULL,
			chat_id INTEGER NOT NULL,
			user_id INTEGER NOT NULL,
			action_type TEXT NOT NULL,
			payload TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			expires_at DATETIME NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS audit_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			workshop_id INTEGER NOT NULL,
			entity_type TEXT NOT NULL,
			entity_id INTEGER NOT NULL,
			actor_user_id INTEGER,
			actor_name TEXT,
			action TEXT NOT NULL,
			field_name TEXT,
			old_value TEXT,
			new_value TEXT,
			details TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
	}
}
