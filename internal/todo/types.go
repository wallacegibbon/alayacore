package todo

// TodoItem represents a single todo item
type TodoItem struct {
	Content    string `json:"content"`
	ActiveForm string `json:"active_form"`
	Status     string `json:"status"` // pending, in_progress, completed
}

// TodoList represents the todo list
type TodoList []TodoItem
