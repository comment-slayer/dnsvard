create table if not exists demo_values (
  id serial primary key,
  value text not null
);

insert into demo_values (value)
select md5(random()::text)
from generate_series(1, 10);
