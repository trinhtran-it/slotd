// Package availability computes free slots from weekly availability rules
// minus active bookings. It is pure: no database, no clock, no I/O.
//
// Rules are local wall-clock times in the provider's IANA time zone; all
// computed windows and slots are absolute instants (UTC).
package availability
