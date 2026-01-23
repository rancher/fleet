package gerrit

type Envelope struct {
	Submitter      User      `json:"-"`
	NewRev         string    `json:"-"`
	PatchSet       PatchSet  `json:"-"`
	Change         Change    `json:"-"`
	Project        Project   `json:"-"`
	RefName        string    `json:"-"`
	ChangeKey      ChangeKey `json:"-"`
	Type           string    `json:"type"`
	EventCreatedOn int64     `json:"-"`
}

type ChangeMergedPayload struct {
	Submitter      User      `json:"submitter"`
	NewRev         string    `json:"newRev"`
	PatchSet       PatchSet  `json:"patchSet"`
	Change         Change    `json:"change"`
	Project        Project   `json:"project"`
	RefName        string    `json:"refName"`
	ChangeKey      ChangeKey `json:"changeKey"`
	Type           string    `json:"type"`
	EventCreatedOn int64     `json:"eventCreatedOn"`
}

type User struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Username string `json:"username"`
}

type PatchSet struct {
	Number         int64    `json:"number"`
	Revision       string   `json:"revision"`
	Parents        []string `json:"parents"`
	Ref            string   `json:"ref"`
	Uploader       User     `json:"uploader"`
	CreatedOn      int64    `json:"createdOn"`
	Author         User     `json:"author"`
	Kind           string   `json:"kind"`
	SizeInsertions int64    `json:"sizeInsertions"`
	SizeDeletions  int64    `json:"sizeDeletions"`
}

type Change struct {
	Project       string `json:"project"`
	Branch        string `json:"branch"`
	ID            string `json:"id"`
	Number        int64  `json:"number"`
	Subject       string `json:"subject"`
	Owner         User   `json:"owner"`
	URL           string `json:"url"`
	CommitMessage string `json:"commitMessage"`
	CreatedOn     int64  `json:"createdOn"`
	Status        string `json:"status"`
}

type Project struct {
	Name string `json:"name"`
}

type ChangeKey struct {
	Key string `json:"key"`
}
