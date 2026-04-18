package database

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"

	"cloud.google.com/go/cloudsqlconn"
	cloudsqlpgxv5 "cloud.google.com/go/cloudsqlconn/postgres/pgxv5"
	"github.com/jackc/pgx/v5"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

const (
	DialerPostgres = "postgres"
	DialerCloudSQL = "cloudsql"
)

type ConnectConfig struct {
	DatabaseURL                    string
	Dialer                         string
	CloudSQLInstanceConnectionName string
	CloudSQLUseIAMAuthN            bool
}

type Handle struct {
	GormDB *gorm.DB
	SQLDB  *sql.DB
}

type Connector struct {
	cfg        ConnectConfig
	driverName string
	cleanup    func() error
}

var cloudSQLDriverSeq atomic.Uint64

func NewConnector(cfg ConnectConfig) (*Connector, error) {
	mode := normalizedDialer(cfg.Dialer)
	if mode == "" {
		mode = DialerPostgres
	}
	connector := &Connector{cfg: cfg}
	if IsSQLiteURL(cfg.DatabaseURL) || mode == DialerPostgres {
		return connector, nil
	}
	if mode != DialerCloudSQL {
		return nil, fmt.Errorf("unsupported DB_DIALER %q", cfg.Dialer)
	}

	var opts []cloudsqlconn.Option
	if cfg.CloudSQLUseIAMAuthN {
		opts = append(opts, cloudsqlconn.WithIAMAuthN())
	}

	driverName := fmt.Sprintf("ghreplica-cloudsql-%d", cloudSQLDriverSeq.Add(1))
	cleanup, err := cloudsqlpgxv5.RegisterDriver(driverName, opts...)
	if err != nil {
		return nil, err
	}
	connector.driverName = driverName
	connector.cleanup = cleanup
	return connector, nil
}

func (c *Connector) Open(poolConfig PoolConfig) (*Handle, error) {
	gormConfig := newGormConfig()

	var (
		db    *gorm.DB
		sqlDB *sql.DB
		err   error
	)

	switch {
	case IsSQLiteURL(c.cfg.DatabaseURL):
		db, err = gorm.Open(sqliteDialector(c.cfg.DatabaseURL), gormConfig)
		if err != nil {
			return nil, err
		}
		sqlDB, err = db.DB()
		if err != nil {
			return nil, err
		}
	case normalizedDialer(c.cfg.Dialer) == DialerCloudSQL:
		dsn, err := buildCloudSQLDSN(c.cfg.DatabaseURL, c.cfg.CloudSQLInstanceConnectionName)
		if err != nil {
			return nil, err
		}
		sqlDB, err = sql.Open(c.driverName, dsn)
		if err != nil {
			return nil, err
		}
		db, err = gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), gormConfig)
		if err != nil {
			_ = sqlDB.Close()
			return nil, err
		}
	default:
		db, err = gorm.Open(postgresDialector(c.cfg.DatabaseURL), gormConfig)
		if err != nil {
			return nil, err
		}
		sqlDB, err = db.DB()
		if err != nil {
			return nil, err
		}
	}

	poolConfig = poolConfig.withDefaults()
	sqlDB.SetMaxOpenConns(poolConfig.MaxOpenConns)
	sqlDB.SetMaxIdleConns(poolConfig.MaxIdleConns)
	sqlDB.SetConnMaxIdleTime(poolConfig.ConnMaxIdleTime)
	sqlDB.SetConnMaxLifetime(poolConfig.ConnMaxLifetime)

	return &Handle{
		GormDB: db,
		SQLDB:  sqlDB,
	}, nil
}

func (c *Connector) Close() error {
	if c.cleanup == nil {
		return nil
	}
	return c.cleanup()
}

func buildCloudSQLDSN(databaseURL, instanceConnectionName string) (string, error) {
	if strings.TrimSpace(instanceConnectionName) == "" {
		return "", errors.New("CLOUDSQL_INSTANCE_CONNECTION_NAME is required for DB_DIALER=cloudsql")
	}

	cfg, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return "", err
	}

	params := []string{
		postgresKeywordValue("host", instanceConnectionName),
		postgresKeywordValue("sslmode", "disable"),
	}
	if cfg.User != "" {
		params = append(params, postgresKeywordValue("user", cfg.User))
	}
	if cfg.Password != "" {
		params = append(params, postgresKeywordValue("password", cfg.Password))
	}
	if cfg.Database != "" {
		params = append(params, postgresKeywordValue("dbname", cfg.Database))
	}
	if cfg.ConnectTimeout > 0 {
		params = append(params, postgresKeywordValue("connect_timeout", strconv.Itoa(int(cfg.ConnectTimeout.Seconds()))))
	}

	keys := make([]string, 0, len(cfg.RuntimeParams))
	for key := range cfg.RuntimeParams {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		params = append(params, postgresKeywordValue(key, cfg.RuntimeParams[key]))
	}

	return strings.Join(params, " "), nil
}

func postgresKeywordValue(key, value string) string {
	needsQuoting := false
	for _, r := range value {
		if r == '\'' || r == '\\' || r == ' ' || r == '\t' || r == '\n' {
			needsQuoting = true
			break
		}
	}
	if !needsQuoting {
		return key + "=" + value
	}

	escaped := strings.ReplaceAll(value, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `'`, `\'`)
	return key + "='" + escaped + "'"
}

func normalizedDialer(mode string) string {
	mode = strings.TrimSpace(strings.ToLower(mode))
	if mode == "" {
		return DialerPostgres
	}
	return mode
}
