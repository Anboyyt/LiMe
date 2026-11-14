-- 年份统一以 TMDB 的首播/上映日期为准。
-- 之前 production_year 来自目录名，和 TMDB release_date/first_air_date
-- 不一致时（例如《米纳里》目录 2021、TMDB 2020-12-11）排序会错位。
-- 这里只回填电影/剧集；单集和季的年份不参与列表排序，
-- 没刮削过的条目继续用目录名年份。
UPDATE items
SET production_year = CAST(substr(premiere_date, 1, 4) AS INTEGER)
WHERE type IN ('Movie', 'Series')
  AND substr(premiere_date, 1, 4) GLOB '[0-9][0-9][0-9][0-9]'
  AND CAST(substr(premiere_date, 1, 4) AS INTEGER) BETWEEN 1800 AND 2200
  AND production_year <> CAST(substr(premiere_date, 1, 4) AS INTEGER);
