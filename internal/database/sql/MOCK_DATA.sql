INSERT INTO users (username, email_address, user_role)
VALUES ('johndoe123', 'johndoe@gmail.com', 'admin'),
       ('steve666 ', 'steve666@gmail.com', 'user'),
       ('notUser3', 'peter_donk@gmail.com', 'user'),
       ('lucia_garcia99', 'lucia.garcia@fmail.com', 'user');

INSERT INTO packages (package_name, package_version)
VALUES ('python3', '3.12.10'),
       ('firefox', '139.0.1'),
       ('openconnect', '9.12.0');

INSERT INTO tracking (user_id, package_id, users_version)
VALUES (1, 1, '3.11.5'),
       (2, 1, '3.12.0'),
       (3, 1, '3.12.10'),
       (4, 2, '139.0.0'),
       (1, 2, '139.0.1'),
       (2, 3, '9.11.0'),
       (3, 2, '139.10.10'),
       (4, 3, '9.12.0'),
       (1, 3, '9.9.12');