package main

import (
	"fmt"
	"time"
)

var unique string = fmt.Sprintf("%d", time.Now().Unix())

func undeliverable(addr string) error {
	fmt.Printf("FAIL: %s %s\n", addr, unique)
	return doAddress(addr, "fail")
}

func unsubscribe(addr string) error {
	fmt.Printf("UNSUBSCRIBE: %s %s\n", addr, unique)
	return doAddress(addr, "unsubscribe")
}

func doAddress(addr, col string) error {
	unique := time.Now().String()
	res, err := db.Exec(fmt.Sprintf(`UPDATE sendlist SET %s = ? WHERE email = ?`, col), unique, addr)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 1 {
		fmt.Printf("Marked %s as %s in the DB\n", addr, col)
	} else {
		_, err := db.Exec(fmt.Sprintf(`INSERT INTO sendlist (email, %s) VALUES (?, ?)`, col), addr, unique)
		if err != nil {
			return fmt.Errorf("No update and error %s: %s\n", addr, err.Error())
		} else {
			fmt.Printf("Added new %s %s\n", col, addr)
		}
	}
	return nil
}