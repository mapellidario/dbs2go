MERGE INTO {{.Owner}}.FILE_LUMIS x
USING (SELECT RUN_NUM,LUMI_SECTION_NUM,FILE_ID,EVENT_COUNT FROM {{.Owner}}.TEMP_FILE_LUMIS ) y
ON (x.RUN_NUM=y.RUN_NUM AND x.LUMI_SECTION_NUM=y.LUMI_SECTION_NUM AND x.FILE_ID=y.FILE_ID)
WHEN NOT MATCHED THEN
    INSERT(x.RUN_NUM, x.LUMI_SECTION_NUM, x.FILE_ID, x.EVENT_COUNT)
    VALUES(y.RUN_NUM, y.LUMI_SECTION_NUM, y.FILE_ID, y.EVENT_COUNT)
