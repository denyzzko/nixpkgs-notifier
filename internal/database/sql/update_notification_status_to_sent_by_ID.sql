UPDATE notifications
SET 
    status = 'sent'
WHERE id = $1
;