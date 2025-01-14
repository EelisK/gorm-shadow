package gormshadow_test

import (
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	gormshadow "github.com/EelisK/gorm-shadow"
	"github.com/stretchr/testify/suite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type mockRow struct {
	ID    int    `gorm:"primaryKey"`
	Value string `gorm:"column:value"`
}

func (m mockRow) ShadowTable() string {
	return "shadow_table"
}

type shadowSuite struct {
	suite.Suite
	db   *gorm.DB
	mock sqlmock.Sqlmock
}

func TestShadowSuite(t *testing.T) {
	suite.Run(t, new(shadowSuite))
}

func (s *shadowSuite) SetupSuite() {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	s.NoError(err)
	s.mock = mock

	s.db, err = gorm.Open(postgres.New(postgres.Config{
		Conn: db,
	}), &gorm.Config{})
	s.NoError(err)
}

func (s *shadowSuite) TearDownSuite() {
	rawDB, err := s.db.DB()
	s.NoError(err)
	s.mock.ExpectClose()
	s.NoError(rawDB.Close())
}

func (s *shadowSuite) TestInitialize() {
	plugin := &gormshadow.Plugin{}
	err := plugin.Initialize(s.db)
	s.NoError(err)
}

func (s *shadowSuite) TestCreateShadow() {
	plugin := &gormshadow.Plugin{}
	err := plugin.Initialize(s.db)
	s.NoError(err)

	addRow := sqlmock.NewRows([]string{"id"}).AddRow("1")
	s.mock.ExpectBegin()
	s.mock.ExpectQuery("INSERT INTO \"mock_rows\" (.+) VALUES (.+)").WillReturnRows(addRow)

	s.mock.ExpectExec("INSERT INTO \"shadow_table\" (.+) VALUES (.+)").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	s.mock.ExpectCommit()

	entry := mockRow{Value: "test"}
	err = s.db.Create(&entry).Error
	s.NoError(err)
}

func (s *shadowSuite) TestCreateWithoutShadowing() {
	type normalRow struct {
		ID int `gorm:"primaryKey"`
	}
	plugin := &gormshadow.Plugin{}
	err := plugin.Initialize(s.db)
	s.NoError(err)

	addRow := sqlmock.NewRows([]string{"id"}).AddRow("1")
	s.mock.ExpectBegin()
	s.mock.ExpectQuery("INSERT INTO \"normal_rows\" (.+) VALUES (.+)").WillReturnRows(addRow)
	s.mock.ExpectCommit()

	entry := normalRow{}
	err = s.db.Create(&entry).Error
	s.NoError(err)
}
