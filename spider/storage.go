package spider

type Storage interface {
	Save(ds ...*DataCell) error
}

type DataCell struct {
	Data map[string]interface{}
	Task *Task
}

func (d *DataCell) GetTableName() string {
	return d.Data["Task"].(string)
}

func (d *DataCell) GetTaskName() string {
	return d.Data["Task"].(string)
}
